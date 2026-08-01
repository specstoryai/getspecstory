import SwiftUI
import SpecStoryKit

/// The Granola-style context panel that opens above the Ask bar (and inside
/// the search overlay) when @ is typed: time pills, agent pills, and a
/// projects list, all filtered by the active token.
struct MentionPanel: View {
    @ObservedObject var model: AppModel
    @ObservedObject var state: MentionState

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Add context")
                    .font(Theme.body(13, weight: .semibold))
                    .foregroundStyle(Theme.ink)
                if let query = state.activeQuery, !query.isEmpty {
                    Text("matching \"\(query)\"")
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                }
                Spacer()
                Button {
                    state.dismissPopover()
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(Theme.inkTertiary)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Close context panel")
            }
            .padding(.horizontal, 18)
            .padding(.top, 14)
            .padding(.bottom, 10)

            sectionLabel("Time")
            HStack(spacing: 8) {
                ForEach(timeMentions, id: \.value) { option in
                    pill(
                        label: option.label,
                        systemImage: "clock",
                        selected: state.timeFilter == option.value
                    ) {
                        if state.timeFilter == option.value {
                            state.removeChip(kind: .time, value: option.value)
                        } else {
                            state.selectKeepingPanel(MentionItem(
                                id: "time-\(option.value)", kind: .time,
                                value: option.value, label: option.label
                            ))
                        }
                    }
                }
            }
            .padding(.horizontal, 18)
            .padding(.bottom, 12)

            sectionLabel("Agents")
            ChipWrapLayout(spacing: 8) {
                ForEach(agentCandidates, id: \.self) { agent in
                    let selected = state.selectedAgents.contains(agent)
                    Button {
                        if selected {
                            state.removeChip(kind: .agent, value: agent)
                        } else {
                            state.selectKeepingPanel(MentionItem(
                                id: "agent-\(agent)", kind: .agent, value: agent, label: agent
                            ))
                        }
                    } label: {
                        HStack(spacing: 6) {
                            ProviderIcon(provider: Provider(providerID: agent), fallbackName: agent, size: 18)
                            Text(agent)
                                .font(Theme.body(11, weight: selected ? .semibold : .regular))
                                .foregroundStyle(selected ? Theme.ink : Theme.inkSecondary)
                            if selected {
                                Image(systemName: "checkmark")
                                    .font(.system(size: 8, weight: .bold))
                                    .foregroundStyle(Theme.accent)
                            }
                        }
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(selected ? Theme.sidebarSelection : Theme.card, in: Capsule())
                        .overlay(Capsule().strokeBorder(selected ? Theme.accent.opacity(0.4) : Theme.hairline))
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 18)
            .padding(.bottom, 12)

            if !projectCandidates.isEmpty {
                sectionLabel("Projects")
                ScrollView {
                    LazyVStack(spacing: 2) {
                        ForEach(projectCandidates, id: \.id) { project in
                            projectRow(project)
                        }
                    }
                    .padding(.horizontal, 10)
                    .padding(.bottom, 10)
                }
                // Explicit height: inside overlays and tight layouts a bare
                // maxHeight lets the scroll view collapse to nothing.
                .frame(height: min(280, CGFloat(projectCandidates.count) * 38 + 14))
            }
        }
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Theme.hairline))
        .shadow(color: .black.opacity(0.14), radius: 24, y: 8)
    }

    // MARK: Candidates

    private var agentCandidates: [String] {
        let query = (state.activeQuery ?? "").lowercased()
        let all = Provider.allCases.map(\.displayName)
        guard !query.isEmpty else { return all }
        return all.filter { $0.lowercased().contains(query) }
    }

    private struct ProjectCandidate: Identifiable {
        let id: String
        let name: String
        let lastUpdated: Date?
    }

    private var projectCandidates: [ProjectCandidate] {
        let query = (state.activeQuery ?? "").lowercased()
        return model.cloudProjects
            .filter { query.isEmpty || $0.name.lowercased().contains(query) }
            .prefix(40)
            .map { ProjectCandidate(id: $0.id, name: $0.name, lastUpdated: $0.lastUpdatedDate) }
    }

    // MARK: Pieces

    private func sectionLabel(_ text: String) -> some View {
        Text(text)
            .font(Theme.body(10, weight: .semibold))
            .foregroundStyle(Theme.inkTertiary)
            .textCase(.uppercase)
            .kerning(0.4)
            .padding(.horizontal, 18)
            .padding(.bottom, 6)
    }

    private func pill(label: String, systemImage: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Image(systemName: systemImage)
                    .font(.system(size: 9))
                Text(label)
                    .font(Theme.body(11, weight: selected ? .semibold : .regular))
                if selected {
                    Image(systemName: "checkmark")
                        .font(.system(size: 8, weight: .bold))
                        .foregroundStyle(Theme.accent)
                }
            }
            .foregroundStyle(selected ? Theme.ink : Theme.inkSecondary)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(selected ? Theme.sidebarSelection : Theme.card, in: Capsule())
            .overlay(Capsule().strokeBorder(selected ? Theme.accent.opacity(0.4) : Theme.hairline))
        }
        .buttonStyle(.plain)
    }

    private func projectRow(_ project: ProjectCandidate) -> some View {
        let selected = state.selectedProjectIDs.contains(project.id)
        return Button {
            if selected {
                state.removeChip(kind: .project, value: project.id)
            } else {
                state.select(MentionItem(
                    id: "project-\(project.id)", kind: .project,
                    value: project.id, label: project.name
                ))
            }
        } label: {
            HStack(spacing: 10) {
                Image(systemName: "folder")
                    .font(.system(size: 12))
                    .foregroundStyle(Theme.accent)
                    .frame(width: 18)
                Text(project.name)
                    .font(Theme.body(13))
                    .foregroundStyle(Theme.ink)
                    .lineLimit(1)
                Spacer()
                if let updated = project.lastUpdated {
                    Text(updated.formatted(.relative(presentation: .named)))
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                }
                if selected {
                    Image(systemName: "checkmark")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(Theme.accent)
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .contentShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            .background(
                selected ? Theme.sidebarSelection : Color.clear,
                in: RoundedRectangle(cornerRadius: 8, style: .continuous)
            )
        }
        .buttonStyle(.plain)
    }
}
