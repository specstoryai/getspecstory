import Foundation

// Pure helpers for the @-mention typeahead, ported from the web client
// (src/webview/lib/mentionText.ts + mentions.ts). Choosing a candidate STRIPS
// the @token from the text and stores the selection as a chip/filter, never as
// text. Kept dependency-free so they unit-test under `swift test`.
//
// Caret positions are Character offsets into the string (the web counterpart
// uses UTF-16 offsets; for the ASCII-and-emoji text these fields hold, the
// semantics match). Out-of-range carets clamp.

// MARK: - Kinds and items

/// The @-reference dimensions. Branch is intentionally absent, matching the
/// web client: repo/branch identity is owned elsewhere.
public enum MentionKind: String, CaseIterable, Equatable, Sendable {
    case project
    case agent
    case time
}

public struct MentionItem: Identifiable, Equatable, Sendable {
    /// Stable list key, e.g. "project-<id>".
    public let id: String
    public let kind: MentionKind
    /// The value stored in the corresponding filter (project id, agent name,
    /// or time filter keyword).
    public let value: String
    public let label: String

    public init(id: String, kind: MentionKind, value: String, label: String) {
        self.id = id
        self.kind = kind
        self.value = value
        self.label = label
    }
}

/// Fixed time-window options, in menu order. Values are the cloud API's
/// timeFilter keywords.
public let timeMentions: [(value: String, label: String)] = [
    (value: "today", label: "Today"),
    (value: "week", label: "Past week"),
    (value: "month", label: "Past month"),
]

// MARK: - Active token detection

/// An @ that starts a token (preceded by start or whitespace) with no spaces
/// or further @s after it, ending at the caret.
private let mentionTokenRegex = try! NSRegularExpression(pattern: "(?:^|\\s)@([^\\s@]*)$")

private func clampedCaret(_ value: String, _ caret: Int) -> Int {
    min(max(caret, 0), value.count)
}

/// The trailing "@token" the caret is currently inside, or nil. Drives the
/// popover open/close.
public func activeMentionQuery(_ value: String, caret: Int) -> String? {
    let upToCaret = String(value.prefix(clampedCaret(value, caret)))
    let ns = upToCaret as NSString
    let range = NSRange(location: 0, length: ns.length)
    guard let match = mentionTokenRegex.firstMatch(in: upToCaret, range: range) else {
        return nil
    }
    return ns.substring(with: match.range(at: 1))
}

/// Remove the active "@token" at the caret (the chip carries the selection,
/// not the text). A token preceded by whitespace keeps that whitespace char;
/// text after the caret is preserved untouched. No active token: identity.
public func stripActiveMention(_ value: String, caret: Int) -> String {
    let clamped = clampedCaret(value, caret)
    let upToCaret = String(value.prefix(clamped))
    let rest = String(value.dropFirst(clamped))
    let ns = upToCaret as NSString
    let range = NSRange(location: 0, length: ns.length)
    guard let match = mentionTokenRegex.firstMatch(in: upToCaret, range: range) else {
        return upToCaret + rest
    }
    let full = ns.substring(with: match.range)
    let replacement = full.hasPrefix("@") ? "" : String(full.prefix(1))
    return ns.replacingCharacters(in: match.range, with: replacement) + rest
}

// MARK: - Candidates

/// All mentionable items for the current selection state, filtered by the
/// active token's query. Already-selected values are excluded. Matching is
/// case-insensitive on the label, prefix matches ranking above contains
/// matches within each kind. Order: projects, then agents, then time windows.
/// Duplicated inputs collapse to one item; the result caps at 12.
public func mentionCandidates(
    projects: [(id: String, name: String)],
    agents: [String],
    selectedProjects: Set<String>,
    selectedAgents: Set<String>,
    timeSelected: Bool,
    query: String
) -> [MentionItem] {
    let maxCandidates = 12
    let q = query.lowercased()

    /// 0 = prefix match, 1 = contains match, nil = no match. The empty query
    /// matches everything as a prefix.
    func matchRank(_ label: String) -> Int? {
        guard !q.isEmpty else { return 0 }
        let lower = label.lowercased()
        if lower.hasPrefix(q) { return 0 }
        if lower.contains(q) { return 1 }
        return nil
    }

    var seen = Set<String>()
    var items = [MentionItem]()

    func appendKind(_ candidates: [MentionItem]) {
        var prefixMatches = [MentionItem]()
        var containsMatches = [MentionItem]()
        for item in candidates {
            guard let rank = matchRank(item.label), seen.insert(item.id).inserted else { continue }
            if rank == 0 {
                prefixMatches.append(item)
            } else {
                containsMatches.append(item)
            }
        }
        items.append(contentsOf: prefixMatches)
        items.append(contentsOf: containsMatches)
    }

    appendKind(projects
        .filter { !selectedProjects.contains($0.id) }
        .map { MentionItem(id: "project-\($0.id)", kind: .project, value: $0.id, label: $0.name) })

    appendKind(agents
        .filter { !selectedAgents.contains($0) }
        .map { MentionItem(id: "agent-\($0)", kind: .agent, value: $0, label: $0) })

    if !timeSelected {
        appendKind(timeMentions
            .map { MentionItem(id: "time-\($0.value)", kind: .time, value: $0.value, label: $0.label) })
    }

    return Array(items.prefix(maxCandidates))
}
