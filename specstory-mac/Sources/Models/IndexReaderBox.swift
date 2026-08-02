import Foundation
import SpecStoryKit

/// Actor confinement for the SQLite reader: one connection, serialized
/// access, always off the main actor (queries against a multi-gigabyte
/// sessions.db can stall for seconds in SQLite cache management).
actor IndexReaderBox {
    private var reader: SessionIndexReader?

    func reopen() {
        reader = try? SessionIndexReader(databaseURL: SessionIndexReader.defaultDatabaseURL)
    }

    var isOpen: Bool { reader != nil }

    func count() -> Int? {
        guard let reader else { return nil }
        return try? reader.count()
    }

    func recentSessions(limit: Int) -> [IndexedSession] {
        guard let reader else { return [] }
        return (try? reader.recentSessions(limit: limit)) ?? []
    }

    func search(_ text: String, limit: Int) -> [IndexedSession] {
        guard let reader else { return [] }
        return (try? reader.search(text, limit: limit)) ?? []
    }

    func searchWithSnippets(_ text: String, limit: Int, snippetLimit: Int) -> [SessionIndexReader.SnippetHit] {
        guard let reader else { return [] }
        return (try? reader.searchWithSnippets(text, limit: limit, snippetLimit: snippetLimit)) ?? []
    }

    func searchLens(matchExpression: String, limit: Int, snippetLimit: Int) -> [SessionIndexReader.SnippetHit] {
        guard let reader else { return [] }
        return (try? reader.searchWithSnippets(matchExpression: matchExpression, limit: limit, snippetLimit: snippetLimit)) ?? []
    }

    func distinctProjectPaths(limit: Int) -> [String] {
        guard let reader else { return [] }
        return (try? reader.distinctProjectPaths(limit: limit)) ?? []
    }
}
