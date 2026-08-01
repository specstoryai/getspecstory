import AppKit
import SwiftUI
import SpecStoryKit

/// The library filter pills, mirroring the web workspace: All / Review /
/// Ready / Installed.
enum SkillLibraryFilter: String, CaseIterable {
    case all, review, ready, installed

    var label: String {
        switch self {
        case .all: return "All"
        case .review: return "Review"
        case .ready: return "Ready"
        case .installed: return "Installed"
        }
    }

    func matches(_ skill: SkillRow) -> Bool {
        switch self {
        case .all: return true
        default: return skill.state == rawValue
        }
    }
}

/// The Library tab: overview counts, filter pills, search, and the skill
/// list with review actions and install shortcuts.
struct SkillLibraryPanel: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel
    let skills: [SkillRow]
    let isPreview: Bool

    @State private var filter: SkillLibraryFilter = .all
    @State private var query = ""

    private func count(_ filter: SkillLibraryFilter) -> Int {
        skills.filter { filter.matches($0) }.count
    }

    /// Review first, then Ready, then Installed; alphabetical within.
    private var visibleSkills: [SkillRow] {
        func rank(_ skill: SkillRow) -> Int {
            switch skill.state {
            case "review": return 0
            case "installed": return 2
            default: return 1
            }
        }
        let needle = query.trimmingCharacters(in: .whitespaces).lowercased()
        return skills
            .filter { filter.matches($0) }
            .filter { skill in
                guard !needle.isEmpty else { return true }
                if skill.name.lowercased().contains(needle) { return true }
                return skill.description?.lowercased().contains(needle) ?? false
            }
            .sorted { a, b in
                let ra = rank(a)
                let rb = rank(b)
                if ra != rb { return ra < rb }
                return a.name < b.name
            }
    }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 10) {
                intro
                overviewStrip
                controls
                    .padding(.bottom, 2)

                if !isPreview, pro.skillsLoading, skills.isEmpty {
                    skeletons
                } else if !isPreview, let error = pro.skillsError, skills.isEmpty {
                    errorCard(error)
                } else if visibleSkills.isEmpty {
                    emptyState
                } else {
                    ForEach(visibleSkills) { skill in
                        SkillLibraryRow(pro: pro, skill: skill, isPreview: isPreview) {
                            pro.selectedSkill = skill
                        }
                    }
                }
            }
            .padding(.horizontal, 28)
            .padding(.vertical, 16)
            .frame(maxWidth: Theme.feedWidth)
            .frame(maxWidth: .infinity)
        }
    }

    private var intro: some View {
        Text("Skills mined from your synced sessions and checked cross-vendor. Confirmed ones join your library automatically; borderline ones wait here for a quick review.")
            .font(Theme.body(12))
            .foregroundStyle(Theme.inkSecondary)
            .lineSpacing(2)
    }

    private var overviewStrip: some View {
        HStack(spacing: 0) {
            countText(count(.review), color: SkillsStyle.amber)
            plainText(" need review   ·   ")
            countText(count(.ready), color: Theme.accent)
            plainText(" ready   ·   ")
            countText(count(.installed), color: Theme.synced)
            plainText(" installed")
        }
        .padding(.top, 2)
    }

    private func countText(_ value: Int, color: Color) -> Text {
        Text("\(value)").font(Theme.body(12, weight: .semibold)).foregroundStyle(color)
    }

    private func plainText(_ string: String) -> Text {
        Text(string).font(Theme.body(12)).foregroundStyle(Theme.inkTertiary)
    }

    private var controls: some View {
        HStack(spacing: 8) {
            ForEach(SkillLibraryFilter.allCases, id: \.self) { candidate in
                filterPill(candidate)
            }
            Spacer()
            searchField
        }
        .padding(.top, 4)
    }

    private func filterPill(_ candidate: SkillLibraryFilter) -> some View {
        let active = filter == candidate
        return Button {
            filter = candidate
        } label: {
            HStack(spacing: 4) {
                Text(candidate.label)
                Text("\(count(candidate))")
                    .foregroundStyle(active ? Theme.paper.opacity(0.8) : Theme.inkTertiary)
            }
            .font(Theme.body(11, weight: active ? .semibold : .regular))
            .foregroundStyle(active ? Theme.paper : Theme.inkSecondary)
            .padding(.horizontal, 9)
            .padding(.vertical, 4)
            .background(active ? AnyShapeStyle(Theme.ink) : AnyShapeStyle(Theme.card), in: Capsule())
            .overlay(Capsule().strokeBorder(active ? Color.clear : Theme.hairline, lineWidth: 1))
        }
        .buttonStyle(.tactile)
    }

    private var searchField: some View {
        HStack(spacing: 5) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 10))
                .foregroundStyle(Theme.inkTertiary)
            TextField("Search skills", text: $query)
                .textFieldStyle(.plain)
                .font(Theme.body(11.5))
                .frame(width: 140)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(Theme.card, in: Capsule())
        .overlay(Capsule().strokeBorder(Theme.hairline, lineWidth: 1))
    }

    private var skeletons: some View {
        ForEach(0..<3, id: \.self) { _ in
            RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
                .fill(Theme.cardHover)
                .frame(height: 66)
                .overlay(
                    RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
                        .strokeBorder(Theme.hairline, lineWidth: 1)
                )
        }
    }

    private func errorCard(_ message: String) -> some View {
        VStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 24, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text(message)
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
            Button("Try again") {
                Task { await pro.refreshSkills() }
            }
            .buttonStyle(.bordered)
            .font(Theme.body(12))
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 40)
    }

    private var emptyState: some View {
        let heading: String
        let body: String
        switch filter {
        case .review:
            heading = "Nothing to review"
            body = "Confirmed skills join your library automatically; only borderline ones land here."
        case .installed:
            heading = "Nothing installed yet"
            body = "Install ready skills to disk with the SpecStory CLI or extension."
        default:
            heading = "No skills yet"
            body = "Skills appear here as background runs mine and confirm them. Install them to disk from the SpecStory CLI or extension."
        }
        return VStack(spacing: 10) {
            Image(systemName: "wand.and.stars")
                .font(.system(size: 28, weight: .light))
                .foregroundStyle(Theme.accent)
            Text(heading)
                .font(Theme.display(19))
                .foregroundStyle(Theme.ink)
            Text(body)
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 40)
    }
}

/// One skill in the library: state glyph, name plus badges, trigger line,
/// minted date, and quick actions (Keep or Dismiss for review rows, a copy
/// install shortcut for the rest). Opens the detail sheet on click.
private struct SkillLibraryRow: View {
    @ObservedObject var pro: ProModel
    let skill: SkillRow
    let isPreview: Bool
    let onOpen: () -> Void

    @State private var hovering = false
    @State private var copiedInstall = false

    private var reviewBusy: Bool {
        guard let dossierID = skill.dossierId else { return false }
        return pro.reviewBusyDossierIDs.contains(dossierID)
    }

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            glyph

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 7) {
                    Text(skill.name)
                        .font(Theme.body(13, weight: .semibold))
                        .foregroundStyle(Theme.ink)
                        .lineLimit(1)
                    if let label = SkillsStyle.stateLabel(skill.state) {
                        SkillsChip(label: label, color: SkillsStyle.stateColor(skill.state))
                    }
                    if skill.isLatentTheme {
                        SkillsChip(label: "latent", color: Theme.dynamicColor(
                            light: NSColor(red: 0.50, green: 0.34, blue: 0.78, alpha: 1),
                            dark: NSColor(red: 0.70, green: 0.58, blue: 0.95, alpha: 1)))
                            .help("A latent practice mined from your conversational sessions")
                    }
                    if let date = skill.updatedAtDate {
                        Text(date.formatted(date: .abbreviated, time: .omitted))
                            .font(Theme.body(10.5))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                if let description = skill.description, !description.isEmpty {
                    Text(description)
                        .font(Theme.body(11.5))
                        .foregroundStyle(Theme.inkSecondary)
                        .lineLimit(1)
                }
                if let agents = skill.installedAgents, !agents.isEmpty {
                    HStack(spacing: 4) {
                        Image(systemName: "checkmark.seal")
                            .font(.system(size: 9))
                        Text("Installed in \(agents.joined(separator: ", "))")
                            .lineLimit(1)
                    }
                    .font(Theme.body(10.5))
                    .foregroundStyle(Theme.synced)
                }
            }

            Spacer(minLength: 8)

            actions

            Image(systemName: "chevron.right")
                .font(.system(size: 10, weight: .medium))
                .foregroundStyle(Theme.inkTertiary)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .cardChrome(hovering: hovering)
        .contentShape(Rectangle())
        .onTapGesture(perform: onOpen)
        .onHover { hovering = $0 }
    }

    private var glyph: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 7, style: .continuous)
                .fill(SkillsStyle.stateColor(skill.state).opacity(0.12))
            Image(systemName: SkillsStyle.stateGlyph(skill.state))
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(SkillsStyle.stateColor(skill.state))
        }
        .frame(width: 30, height: 30)
    }

    @ViewBuilder private var actions: some View {
        if skill.state == "review" {
            HStack(spacing: 6) {
                Button("Keep") {
                    Task { await pro.keepSkill(skill) }
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)

                Button("Dismiss") {
                    Task { await pro.dismissSkill(skill) }
                }
                .buttonStyle(.bordered)
            }
            .controlSize(.small)
            .font(Theme.body(11))
            .disabled(isPreview || reviewBusy)
        } else if hovering || copiedInstall {
            Button(copiedInstall ? "Copied" : "Copy install") {
                pro.copyInstallCommand(skill)
                copiedInstall = true
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.6) {
                    copiedInstall = false
                }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .font(Theme.body(11))
            .disabled(isPreview)
        }
    }
}
