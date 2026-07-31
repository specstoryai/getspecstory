import Foundation
import SpecStoryKit

/// Selection and typeahead state behind one @-mention text field (the Ask bar
/// and the search overlay each own an instance). Web parity semantics: an
/// @token at the caret opens the typeahead; choosing a candidate STRIPS the
/// @token from the text and stores the selection as a chip, never as text.
///
/// Caret note: SwiftUI's TextField exposes no caret position and NSTextField
/// field-editor tracking is unreliable through the SwiftUI bridge, so this
/// state tracks the caret as the end of the text. The web client's mid-text
/// caret nuance degrades gracefully: an @token typed mid-text only opens the
/// popover while it is the trailing token, which is the overwhelmingly common
/// case for a one-line field.
@MainActor
final class MentionState: ObservableObject {
    /// The text field's binding.
    @Published var text = ""

    /// Chip state, in selection order.
    @Published private(set) var selectedProjectIDs: [String] = []
    @Published private(set) var selectedAgents: [String] = []
    @Published private(set) var timeFilter: String?

    /// Typeahead state.
    @Published var popoverShown = false
    @Published var highlightedIndex = 0
    @Published private(set) var activeQuery: String?

    /// Display labels captured at selection time, keyed by MentionItem.id, so
    /// the chip row never re-resolves project names.
    @Published private(set) var chipLabels: [String: String] = [:]

    // MARK: Typeahead

    /// Recompute the active @token. Call from the field's onChange. The caret
    /// defaults to the end of the text (see the caret note above).
    func textChanged(caret: Int? = nil) {
        let query = activeMentionQuery(text, caret: caret ?? text.count)
        activeQuery = query
        if query != nil {
            highlightedIndex = 0
            if !popoverShown { popoverShown = true }
        } else if popoverShown {
            popoverShown = false
        }
    }

    /// Candidates for the active token, or empty when no token is active.
    func candidates(projects: [(id: String, name: String)], agents: [String]) -> [MentionItem] {
        guard let query = activeQuery else { return [] }
        return mentionCandidates(
            projects: projects,
            agents: agents,
            selectedProjects: Set(selectedProjectIDs),
            selectedAgents: Set(selectedAgents),
            timeSelected: timeFilter != nil,
            query: query
        )
    }

    /// Choosing a candidate strips the @token from the text and appends chip
    /// state. The selection never lands in the text.
    func select(_ item: MentionItem) {
        text = stripActiveMention(text, caret: text.count)
        chipLabels[item.id] = item.label
        switch item.kind {
        case .project:
            if !selectedProjectIDs.contains(item.value) {
                selectedProjectIDs.append(item.value)
            }
        case .agent:
            if !selectedAgents.contains(item.value) {
                selectedAgents.append(item.value)
            }
        case .time:
            if let previous = timeFilter, previous != item.value {
                chipLabels["time-\(previous)"] = nil
            }
            timeFilter = item.value
        }
        activeQuery = nil
        popoverShown = false
        highlightedIndex = 0
    }

    func removeChip(kind: MentionKind, value: String) {
        switch kind {
        case .project:
            selectedProjectIDs.removeAll { $0 == value }
        case .agent:
            selectedAgents.removeAll { $0 == value }
        case .time:
            if timeFilter == value { timeFilter = nil }
        }
        chipLabels["\(kind.rawValue)-\(value)"] = nil
    }

    func clearAll() {
        selectedProjectIDs = []
        selectedAgents = []
        timeFilter = nil
        chipLabels = [:]
        activeQuery = nil
        popoverShown = false
        highlightedIndex = 0
    }

    func dismissPopover() {
        activeQuery = nil
        popoverShown = false
    }

    // MARK: Keyboard

    /// Wrap-around highlight movement for up/down arrows.
    func moveHighlight(by delta: Int, count: Int) {
        guard count > 0 else { return }
        highlightedIndex = ((highlightedIndex + delta) % count + count) % count
    }

    /// Return-key hook for the field's onSubmit: selects the highlighted
    /// candidate and reports true when the popover consumed the Return, so the
    /// caller submits only on false.
    func consumeReturn(candidates: [MentionItem]) -> Bool {
        guard popoverShown, !candidates.isEmpty else { return false }
        select(candidates[min(max(highlightedIndex, 0), candidates.count - 1)])
        return true
    }

    // MARK: Chip display

    var hasChips: Bool {
        !selectedProjectIDs.isEmpty || !selectedAgents.isEmpty || timeFilter != nil
    }

    /// Chips in display order: projects, then agents, then the time window.
    var chips: [MentionItem] {
        var items = [MentionItem]()
        for id in selectedProjectIDs {
            let key = "project-\(id)"
            items.append(MentionItem(id: key, kind: .project, value: id, label: chipLabels[key] ?? id))
        }
        for name in selectedAgents {
            items.append(MentionItem(id: "agent-\(name)", kind: .agent, value: name, label: name))
        }
        if let time = timeFilter {
            let key = "time-\(time)"
            let label = chipLabels[key] ?? timeMentions.first { $0.value == time }?.label ?? time
            items.append(MentionItem(id: key, kind: .time, value: time, label: label))
        }
        return items
    }

    // MARK: Request parameters

    /// Shapes match ChatStreamClient.ask and CloudAPI.searchSessions: nil when
    /// the dimension has no filter.
    var projectIDsParam: [String]? { selectedProjectIDs.isEmpty ? nil : selectedProjectIDs }
    var agentNamesParam: [String]? { selectedAgents.isEmpty ? nil : selectedAgents }
    var timeFilterParam: String? { timeFilter }
}
