import Foundation
import SwiftUI
import SpecStoryKit

/// Element kinds the transcript filter row can show or hide (cloud parity).
/// Raw values persist in UserDefaults, keep them stable.
enum TranscriptElement: String, CaseIterable, Identifiable {
    case prompts
    case responses
    case thinking
    case fileEdits
    case fileWrites
    case bash
    case otherTools

    var id: String { rawValue }

    var label: String {
        switch self {
        case .prompts: return "Prompts"
        case .responses: return "Responses"
        case .thinking: return "Thinking"
        case .fileEdits: return "File edits"
        case .fileWrites: return "File writes"
        case .bash: return "Bash"
        case .otherTools: return "Other tools"
        }
    }

    /// The filter bucket a parsed segment belongs to. Prose, code, and role
    /// headers on the agent side all count as "Responses"; tools split by the
    /// cloud viewer's category rules.
    static func element(for segment: SessionTranscript.Segment) -> TranscriptElement {
        switch segment {
        case .prose, .code, .roleHeader:
            return .responses
        case .thinking:
            return .thinking
        case .toolUse(let type, let name, _, _):
            switch SessionTranscript.toolCategory(type: type, name: name) {
            case .fileEdit: return .fileEdits
            case .fileWrite: return .fileWrites
            case .bash: return .bash
            case .other: return .otherTools
            }
        }
    }
}

/// View-local state for the session transcript: the parsed exchanges (parsed
/// once, off the main actor), the element filter set (persisted), and the
/// exchange the scroll position currently rests on.
@MainActor
final class TranscriptState: ObservableObject {
    static let filterDefaultsKey = "transcriptElementFilters"

    @Published private(set) var transcript: SessionTranscript?
    @Published private(set) var parsing = false
    @Published var activeExchangeID: Int?
    /// Search deep link: set after parse when a snippet located its exchange;
    /// the content view scrolls to it and clears it.
    @Published var pendingJumpExchangeID: Int?
    /// Snippet awaiting the parse, handed over by the search overlay.
    var pendingSnippet: HighlightedSnippet?
    @Published var visible: Set<TranscriptElement> {
        didSet {
            UserDefaults.standard.set(visible.map(\.rawValue).sorted(), forKey: Self.filterDefaultsKey)
        }
    }

    private var parsedMarkdown: String?
    private var parseTask: Task<Void, Never>?

    init() {
        if let stored = UserDefaults.standard.stringArray(forKey: Self.filterDefaultsKey) {
            let restored = Set(stored.compactMap(TranscriptElement.init(rawValue:)))
            visible = restored.isEmpty ? Set(TranscriptElement.allCases) : restored
        } else {
            visible = Set(TranscriptElement.allCases)
        }
    }

    /// Parses new markdown in a detached task so a megabyte session never
    /// blocks the main actor; results publish back here.
    func update(markdown: String?) {
        guard markdown != parsedMarkdown else {
            // Same session reopened from search: the parse is already done,
            // so consume the deep link against the existing transcript.
            if let snippet = pendingSnippet {
                pendingSnippet = nil
                if let transcript, let target = transcript.locateExchange(matching: snippet) {
                    activeExchangeID = target
                    pendingJumpExchangeID = target
                }
            }
            return
        }
        parsedMarkdown = markdown
        parseTask?.cancel()
        guard let markdown, !markdown.isEmpty else {
            transcript = nil
            parsing = false
            return
        }
        parsing = true
        transcript = nil
        parseTask = Task { [weak self] in
            let parsed = await Task.detached(priority: .userInitiated) {
                SessionTranscript.parse(markdown: markdown)
            }.value
            guard let self, !Task.isCancelled else { return }
            self.transcript = parsed
            self.parsing = false
            self.activeExchangeID = parsed.exchanges.first?.id
            if let snippet = self.pendingSnippet {
                self.pendingSnippet = nil
                if let target = parsed.locateExchange(matching: snippet) {
                    self.activeExchangeID = target
                    self.pendingJumpExchangeID = target
                }
            }
        }
    }

    // MARK: Filters

    var counts: SessionTranscript.ElementCounts {
        transcript?.elementCounts ?? SessionTranscript.ElementCounts()
    }

    func count(for element: TranscriptElement) -> Int {
        let counts = counts
        switch element {
        case .prompts: return counts.prompts
        case .responses: return counts.responses
        case .thinking: return counts.thinking
        case .fileEdits: return counts.fileEdits
        case .fileWrites: return counts.fileWrites
        case .bash: return counts.bash
        case .otherTools: return counts.otherTools
        }
    }

    var allVisible: Bool { visible == Set(TranscriptElement.allCases) }

    var cleanReadingActive: Bool { visible == [.prompts, .responses] }

    func toggle(_ element: TranscriptElement) {
        if visible.contains(element) {
            visible.remove(element)
        } else {
            visible.insert(element)
        }
    }

    /// Cloud parity preset: prompts and responses only.
    func applyCleanReading() {
        visible = [.prompts, .responses]
    }

    func showEverything() {
        visible = Set(TranscriptElement.allCases)
    }
}
