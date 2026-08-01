import AppKit
import SwiftUI
import SpecStoryKit

/// The skill detail sheet: metadata (state, confidence, evidence, cluster
/// provenance), the install command, the rendered SKILL.md, and the actions
/// the state allows (Keep or Dismiss for review skills, install state
/// management and copy for the rest).
struct SkillDetailSheet: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel
    let skill: SkillRow
    @Environment(\.dismiss) private var dismiss

    @State private var copiedCommand = false

    private var isReview: Bool { skill.state == "review" }

    private var reviewBusy: Bool {
        guard let dossierID = skill.dossierId else { return false }
        return pro.reviewBusyDossierIDs.contains(dossierID)
    }

    private var installBusy: Bool {
        pro.installBusySkillNames.contains(skill.name)
    }

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 24)
                .padding(.top, 20)
                .padding(.bottom, 12)

            Divider().overlay(Theme.hairline)

            trustStrip
                .padding(.horizontal, 24)
                .padding(.vertical, 10)

            if !isReview {
                installCommandBlock
                    .padding(.horizontal, 24)
                    .padding(.bottom, 10)
            }

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

            footer
                .padding(.horizontal, 24)
                .padding(.vertical, 14)
        }
        .frame(width: 680, height: 560)
        .background(Theme.paper)
    }

    // MARK: Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text(skill.name)
                    .font(Theme.display(20))
                    .foregroundStyle(Theme.ink)
                    .lineLimit(2)
                if let label = SkillsStyle.stateLabel(skill.state) {
                    SkillsChip(label: label, color: SkillsStyle.stateColor(skill.state))
                }
                if skill.isLatentTheme {
                    SkillsChip(label: "latent", color: Theme.dynamicColor(
                        light: NSColor(red: 0.50, green: 0.34, blue: 0.78, alpha: 1),
                        dark: NSColor(red: 0.70, green: 0.58, blue: 0.95, alpha: 1)))
                        .help("A latent practice mined from your conversational sessions")
                }
                Spacer()
                if let date = skill.updatedAtDate {
                    Text("Minted \(date.formatted(date: .abbreviated, time: .omitted))")
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                }
            }
            if let description = skill.description, !description.isEmpty {
                Text(description)
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                    .lineLimit(2)
            }
            if let agents = skill.installedAgents, !agents.isEmpty {
                HStack(spacing: 4) {
                    Image(systemName: "checkmark.seal")
                        .font(.system(size: 9))
                    Text("Installed in \(agents.joined(separator: ", "))")
                }
                .font(Theme.body(11))
                .foregroundStyle(Theme.synced)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    // MARK: Trust metadata

    private var trustStrip: some View {
        VStack(alignment: .leading, spacing: 5) {
            SkillsSectionCaption(text: "Why you can trust it")
            HStack(spacing: 6) {
                if let confidence = skill.confidence {
                    Text("confidence \(confidence.formatted(.number.precision(.fractionLength(2))))")
                        .font(Theme.body(11, weight: .medium))
                        .foregroundStyle(Theme.ink)
                    dot
                }
                Text("judged cross-vendor")
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkSecondary)
                if let refs = skill.evidenceRefs, !refs.isEmpty {
                    dot
                    Text("\(refs.count) sessions of evidence")
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkSecondary)
                }
                if let cluster = skill.clusterKey, !cluster.isEmpty {
                    dot
                    Text("from cluster \(cluster)")
                        .font(Theme.mono(10.5))
                        .foregroundStyle(Theme.inkTertiary)
                }
                Spacer(minLength: 0)
            }
            if let refs = skill.evidenceRefs, !refs.isEmpty {
                HStack(spacing: 6) {
                    ForEach(refs.prefix(5), id: \.self) { ref in
                        Text(ref)
                            .font(Theme.mono(9.5))
                            .foregroundStyle(Theme.inkTertiary)
                            .lineLimit(1)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Theme.card, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
                            .overlay(
                                RoundedRectangle(cornerRadius: 4, style: .continuous)
                                    .strokeBorder(Theme.hairline, lineWidth: 1)
                            )
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var dot: some View {
        Text("·")
            .font(Theme.body(11))
            .foregroundStyle(Theme.inkTertiary)
    }

    // MARK: Install command

    private var installCommandBlock: some View {
        Button {
            pro.copyInstallCommand(skill)
            copiedCommand = true
            model.showToast("Install command copied")
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.6) {
                copiedCommand = false
            }
        } label: {
            HStack(spacing: 8) {
                Text(pro.installCommand(for: skill))
                    .font(Theme.mono(11.5))
                    .foregroundStyle(Theme.ink)
                    .lineLimit(1)
                Spacer()
                HStack(spacing: 4) {
                    Image(systemName: copiedCommand ? "checkmark" : "doc.on.doc")
                        .font(.system(size: 10))
                    Text(copiedCommand ? "Copied" : "Copy")
                }
                .font(Theme.body(11))
                .foregroundStyle(copiedCommand ? Theme.synced : Theme.inkSecondary)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(Theme.card, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .strokeBorder(Theme.hairline, lineWidth: 1)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.tactile)
        .help("Copy the CLI command that installs this skill to disk")
    }

    // MARK: Footer

    private var footer: some View {
        HStack(spacing: 8) {
            Button {
                pro.copySkillContent(skill)
                model.showToast("Skill content copied")
            } label: {
                Label("Copy content", systemImage: "doc.on.doc")
                    .font(Theme.body(12))
            }
            .buttonStyle(.bordered)
            .disabled(skill.content.isEmpty)

            if !isReview {
                installStateMenu
            }

            Spacer()

            if isReview {
                Button("Dismiss") {
                    Task {
                        await pro.dismissSkill(skill)
                        dismiss()
                    }
                }
                .buttonStyle(.bordered)
                .disabled(reviewBusy)

                Button("Keep") {
                    Task {
                        await pro.keepSkill(skill)
                        dismiss()
                    }
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)
                .keyboardShortcut(.defaultAction)
                .disabled(reviewBusy)

                Button("Done") { dismiss() }
                    .buttonStyle(.bordered)
            } else {
                Button("Done") { dismiss() }
                    .buttonStyle(.borderedProminent)
                    .tint(Theme.accent)
                    .keyboardShortcut(.defaultAction)
            }
        }
    }

    private var installStateMenu: some View {
        Menu {
            Button("Copy install command") {
                pro.copyInstallCommand(skill)
                model.showToast("Install command copied")
            }
            Divider()
            if skill.state == "installed" {
                Button("Mark as not installed") {
                    Task { await pro.setSkillInstalled(skill, installed: false) }
                }
            } else {
                Button("Mark as installed") {
                    Task { await pro.setSkillInstalled(skill, installed: true) }
                }
            }
        } label: {
            if installBusy {
                ProgressView()
                    .controlSize(.small)
            } else {
                Label("Install", systemImage: "arrow.down.circle")
                    .font(Theme.body(12))
            }
        }
        .fixedSize()
        .disabled(installBusy)
        .help("Install with the SpecStory CLI, then the state syncs here")
    }
}
