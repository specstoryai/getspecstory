import Foundation
import SpecStoryKit

/// Curated search lenses: each is a hand-authored FTS5 MATCH expression over
/// the flattened conversation body (heuristics, tuned for recall).
enum SearchLens: String, CaseIterable, Identifiable, Codable {
    case decisions, corrections, errors, commands, unresolved

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .decisions: return "Decisions"
        case .corrections: return "Corrections"
        case .errors: return "Errors"
        case .commands: return "Commands"
        case .unresolved: return "Unresolved"
        }
    }

    var symbol: String {
        switch self {
        case .decisions: return "checkmark.seal"
        case .corrections: return "arrow.uturn.backward"
        case .errors: return "exclamationmark.triangle"
        case .commands: return "terminal"
        case .unresolved: return "circle.dashed"
        }
    }

    /// FTS5 MATCH expression (OR groups; quoted phrases).
    var matchExpression: String {
        switch self {
        case .decisions:
            return "\"we decided\" OR \"decision\" OR \"let us go with\" OR \"going with\" OR \"we will use\" OR \"chose\" OR \"settled on\" OR \"the plan is\""
        case .corrections:
            return "\"actually\" OR \"my mistake\" OR \"that was wrong\" OR \"correction\" OR \"revert\" OR \"undo that\" OR \"instead of\" OR \"scratch that\""
        case .errors:
            return "\"error\" OR \"exception\" OR \"traceback\" OR \"stack trace\" OR \"build failed\" OR \"test failed\" OR \"panic\" OR \"segfault\""
        case .commands:
            return "\"npm run\" OR \"git commit\" OR \"git push\" OR \"cargo\" OR \"pytest\" OR \"xcodebuild\" OR \"docker\" OR \"make\""
        case .unresolved:
            return "\"TODO\" OR \"FIXME\" OR \"not yet\" OR \"still failing\" OR \"follow up\" OR \"unresolved\" OR \"come back to\" OR \"later\""
        }
    }
}

/// A named, persisted search: text plus chips plus optional lens.
struct SavedSearch: Identifiable, Codable, Equatable {
    var id = UUID()
    var name: String
    var query: String
    var lens: SearchLens?
    var projectIDs: [String] = []
    var agents: [String] = []
    var timeFilter: String?

    static func load() -> [SavedSearch] {
        guard let data = UserDefaults.standard.data(forKey: "savedSearches"),
              let saved = try? JSONDecoder().decode([SavedSearch].self, from: data) else { return [] }
        return saved
    }

    static func store(_ searches: [SavedSearch]) {
        UserDefaults.standard.set(try? JSONEncoder().encode(searches), forKey: "savedSearches")
    }
}
