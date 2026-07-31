import AppKit
import SwiftUI
import SpecStoryKit

/// The Skills panel, gate-aware. Enabled shows the mined skills library with
/// a SKILL.md detail sheet; upsell shows the Pro upgrade card; hidden means
/// the feature is dark in this environment.
struct SkillsView: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel
    @State private var detailSkill: SkillRow?

    var body: some View {
        Group {
            switch pro.skillsGate {
            case .enabled:
                library
            case .upsell:
                upsell
            case .hidden:
                ContentUnavailableView(
                    "Skills are not available yet",
                    systemImage: "wand.and.stars",
                    description: Text("Check back soon.")
                )
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.paper)
        .task(id: pro.skillsGate) {
            if pro.skillsGate == .enabled, !pro.skillsLoadedOnce {
                await pro.refreshSkills()
            }
        }
        .sheet(item: $detailSkill) { skill in
            SkillDetailSheet(model: model, pro: pro, skill: skill)
        }
    }

    // MARK: Enabled

    private var library: some View {
        VStack(spacing: 0) {
            header
            if pro.skillsLoading && pro.skills.isEmpty {
                Spacer()
                ProgressView("Loading skills")
                    .controlSize(.small)
                Spacer()
            } else if let error = pro.skillsError, pro.skills.isEmpty {
                Spacer()
                errorState(error)
                Spacer()
            } else if pro.skills.isEmpty {
                Spacer()
                emptyState
                Spacer()
            } else {
                ScrollView {
                    LazyVStack(spacing: 10) {
                        ForEach(pro.skills) { skill in
                            SkillCardView(skill: skill) {
                                detailSkill = skill
                            }
                        }
                    }
                    .padding(.horizontal, 28)
                    .padding(.vertical, 16)
                    .frame(maxWidth: Theme.feedWidth)
                    .frame(maxWidth: .infinity)
                }
            }
        }
    }

    private var header: some View {
        HStack(spacing: 10) {
            Text("Skills")
                .font(Theme.display(24))
                .foregroundStyle(Theme.ink)
            if pro.gatesLoaded {
                Text(pro.plan.displayName)
                    .font(Theme.body(10, weight: .semibold))
                    .foregroundStyle(Theme.accent)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(Theme.accent.opacity(0.12), in: Capsule())
            }
            Spacer()
            if pro.skillsLoading {
                ProgressView().controlSize(.small)
            } else {
                Button {
                    Task { await pro.refreshSkills() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(Theme.inkSecondary)
                }
                .buttonStyle(.plain)
                .help("Refresh skills")
            }
        }
        .padding(.horizontal, 28)
        .padding(.top, 24)
        .padding(.bottom, 12)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "wand.and.stars")
                .font(.system(size: 30, weight: .light))
                .foregroundStyle(Theme.accent)
            Text("No skills yet")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("SpecStory mines your synced sessions into reusable skills. Keep syncing and new skills will appear here.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
    }

    private func errorState(_ message: String) -> some View {
        VStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 26, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text(message)
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
            Button("Try again") {
                Task { await pro.refreshSkills() }
            }
            .buttonStyle(.bordered)
            .font(Theme.body(12))
        }
    }

    // MARK: Upsell

    private var upsell: some View {
        VStack {
            Spacer()
            UpsellCard(
                copy: ProModel.skillsUpsell,
                busy: pro.billingBusy,
                upgradeAction: { pro.openCheckout(plan: .pro) },
                manageAction: model.signedInEmail == nil ? nil : { pro.openPortal() },
                footnote: model.signedInEmail == nil ? "Already on Pro? Sign in to unlock skills here." : nil
            )
            Spacer()
        }
        .frame(maxWidth: .infinity)
    }
}

/// One skill in the library list: name, description, state badge, install
/// targets, and when it last changed.
private struct SkillCardView: View {
    let skill: SkillRow
    let onOpen: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: onOpen) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 8) {
                    Text(skill.name)
                        .font(Theme.body(13, weight: .semibold))
                        .foregroundStyle(Theme.ink)
                        .lineLimit(1)
                    stateBadge
                    Spacer()
                    if let date = skill.updatedAtDate {
                        Text(date.formatted(date: .abbreviated, time: .omitted))
                            .font(Theme.body(11))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                if let description = skill.description, !description.isEmpty {
                    Text(description)
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.inkSecondary)
                        .lineLimit(2)
                        .multilineTextAlignment(.leading)
                }
                if let agents = skill.installedAgents, !agents.isEmpty {
                    HStack(spacing: 5) {
                        Image(systemName: "checkmark.seal")
                            .font(.system(size: 10))
                        Text("Installed in \(agents.joined(separator: ", "))")
                            .lineLimit(1)
                    }
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.synced)
                }
            }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .cardChrome(hovering: hovering)
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }

    @ViewBuilder private var stateBadge: some View {
        switch skill.state {
        case "review":
            badge("Needs review", color: Theme.live)
        case "installed":
            badge("Installed", color: Theme.synced)
        case "ready":
            badge("Ready", color: Theme.accent)
        default:
            EmptyView()
        }
    }

    private func badge(_ label: String, color: Color) -> some View {
        Text(label)
            .font(Theme.body(10, weight: .medium))
            .foregroundStyle(color)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(color.opacity(0.12), in: Capsule())
    }
}

/// Detail sheet: rendered SKILL.md plus a Copy content button.
private struct SkillDetailSheet: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel
    let skill: SkillRow
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 6) {
                Text(skill.name)
                    .font(Theme.display(20))
                    .foregroundStyle(Theme.ink)
                    .lineLimit(2)
                HStack(spacing: 6) {
                    if let description = skill.description, !description.isEmpty {
                        Text(description).lineLimit(1)
                    }
                    if let date = skill.updatedAtDate {
                        if skill.description?.isEmpty == false {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                        }
                        Text("Updated \(date.formatted(date: .abbreviated, time: .shortened))")
                    }
                }
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 24)
            .padding(.top, 20)
            .padding(.bottom, 12)

            Divider().overlay(Theme.hairline)

            if skill.content.isEmpty {
                Spacer()
                Text("This skill has no content yet.")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                Spacer()
            } else {
                RawMarkdownFallbackView(markdown: skill.content)
            }

            Divider().overlay(Theme.hairline)

            HStack {
                Button {
                    pro.copySkillContent(skill)
                    model.showToast("Skill content copied")
                } label: {
                    Label("Copy content", systemImage: "doc.on.doc")
                        .font(Theme.body(12))
                }
                .buttonStyle(.bordered)
                .disabled(skill.content.isEmpty)

                Spacer()

                Button("Done") { dismiss() }
                    .buttonStyle(.borderedProminent)
                    .tint(Theme.accent)
                    .keyboardShortcut(.defaultAction)
            }
            .padding(.horizontal, 24)
            .padding(.vertical, 14)
        }
        .frame(width: 640, height: 540)
        .background(Theme.paper)
    }
}
