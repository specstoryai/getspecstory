import Foundation
import SQLite3

// sqlite3_bind_text needs SQLITE_TRANSIENT so SQLite copies Swift-owned UTF-8
// buffers before they are deallocated; the C macro does not import into Swift.
private let sqliteTransient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

/// One row of the CLI's cross-project session index (`~/.specstory/sessions.db`).
public struct IndexedSession: Equatable, Sendable {
    public let sessionID: String
    /// CLI registry id, e.g. "claude", "codex". Kept as String because the DB
    /// may contain providers this app build does not know yet.
    public let provider: String
    /// The session's originating working directory (`origin_cwd`); empty when
    /// the CLI could not attribute the session to a project.
    public let projectPath: String
    /// Best available display title: name, else slug, else the session id.
    public let title: String
    public let slug: String?
    public let createdAt: Date?
    public let updatedAt: Date?
    public let userPromptCount: Int?
    /// Schema v7 does not store a markdown path; populated only if a future
    /// schema adds a `markdown_path` column.
    public let markdownPath: String?

    public init(
        sessionID: String,
        provider: String,
        projectPath: String,
        title: String,
        slug: String?,
        createdAt: Date?,
        updatedAt: Date?,
        userPromptCount: Int?,
        markdownPath: String?
    ) {
        self.sessionID = sessionID
        self.provider = provider
        self.projectPath = projectPath
        self.title = title
        self.slug = slug
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.userPromptCount = userPromptCount
        self.markdownPath = markdownPath
    }
}

/// Read-only reader over `~/.specstory/sessions.db` (SQLite + FTS5, WAL),
/// the CLI-owned rebuildable session index. Schema v7 per
/// specstory-cli/docs/SESSIONS-DB.md.
///
/// Invariants: opened with SQLITE_OPEN_READONLY (never creates; the sole
/// write-mode touch is a passive WAL-recovery checkpoint after a crashed writer),
/// tolerates a missing file (`ReaderError.databaseMissing`), and tolerates
/// schema drift by probing columns at init and substituting NULL for absent
/// optional columns; empty results beat crashes.
public final class SessionIndexReader {
    public enum ReaderError: Error {
        case databaseMissing
        case sqlite(String)
    }

    private let db: OpaquePointer
    /// Columns actually present in `sessions`, probed at init for drift tolerance.
    private let presentColumns: Set<String>
    private let hasFTS: Bool

    public static var defaultDatabaseURL: URL {
        // Literal tildes are not expanded by the file APIs; resolve the real home.
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".specstory")
            .appendingPathComponent("sessions.db")
    }

    public init(databaseURL: URL) throws {
        let path = databaseURL.path
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &isDirectory),
              !isDirectory.boolValue else {
            throw ReaderError.databaseMissing
        }
        var opened = try Self.openReadonly(path: path)
        var columns = Self.columnNames(db: opened, table: "sessions")
        if columns.isEmpty {
            // A writer killed mid-transaction leaves a WAL that a readonly
            // connection cannot recover (SQLITE_READONLY_RECOVERY): the open
            // succeeds but every query fails, so the index reads as empty.
            // A brief read-write open performs the recovery, then readonly
            // service resumes.
            sqlite3_close(opened)
            Self.recoverWAL(path: path)
            opened = try Self.openReadonly(path: path)
            columns = Self.columnNames(db: opened, table: "sessions")
        }
        db = opened
        presentColumns = columns
        hasFTS = Self.tableExists(db: opened, name: "sessions_fts")
    }

    private static func openReadonly(path: String) throws -> OpaquePointer {
        var handle: OpaquePointer?
        let rc = sqlite3_open_v2(path, &handle, SQLITE_OPEN_READONLY, nil)
        guard rc == SQLITE_OK, let opened = handle else {
            let message = handle.map { String(cString: sqlite3_errmsg($0)) } ?? "sqlite open failed (code \(rc))"
            sqlite3_close(handle)
            throw ReaderError.sqlite(message)
        }
        // The CLI's writer holds WAL write locks briefly; wait instead of erroring.
        sqlite3_busy_timeout(opened, 2000)
        // The index can exceed multiple gigabytes; a large page cache makes
        // SQLite's purgeable-memory shrinking stall for seconds on finalize.
        // Reads here are shallow, so keep the cache tiny (2 MB).
        sqlite3_exec(opened, "PRAGMA cache_size = -2000", nil, nil, nil)
        return opened
    }

    /// The one place this reader touches the database read-write, and only
    /// when the readonly probe found nothing: a passive checkpoint makes
    /// SQLite run WAL recovery after a crashed writer. No schema or row is
    /// ever written.
    private static func recoverWAL(path: String) {
        var handle: OpaquePointer?
        guard sqlite3_open_v2(path, &handle, SQLITE_OPEN_READWRITE, nil) == SQLITE_OK, let rw = handle else {
            sqlite3_close(handle)
            return
        }
        sqlite3_busy_timeout(rw, 2000)
        sqlite3_exec(rw, "PRAGMA wal_checkpoint(PASSIVE)", nil, nil, nil)
        sqlite3_close(rw)
    }

    deinit {
        sqlite3_close(db)
    }

    // MARK: - Public queries

    /// Exact number of live (non-tombstoned) sessions in the index.
    public func count() throws -> Int {
        guard hasCoreColumns else { return 0 }
        let statement = try prepare("SELECT COUNT(*) FROM sessions WHERE \(notDeletedClause(prefix: ""))")
        defer { sqlite3_finalize(statement) }
        guard sqlite3_step(statement) == SQLITE_ROW else { return 0 }
        return Int(sqlite3_column_int64(statement, 0))
    }

    /// Sessions ordered most recently active first.
    public func recentSessions(limit: Int) throws -> [IndexedSession] {
        guard hasCoreColumns else { return [] }
        let sql = """
        SELECT \(selectList(prefix: "")) FROM sessions
        WHERE \(notDeletedClause(prefix: ""))
        ORDER BY \(recencyExpression(prefix: "")) DESC
        LIMIT ?
        """
        return try querySessions(sql: sql, textBindings: [], limit: limit)
    }

    /// Full-text search. The user text is passed to FTS5 MATCH as a bound
    /// parameter (never interpolated into SQL); FTS5 rejects punctuation-heavy
    /// or unbalanced queries with a syntax error, in which case this falls back
    /// to a plain LIKE substring scan so the user always gets results.
    public func search(_ text: String, limit: Int) throws -> [IndexedSession] {
        try searchWithSnippets(text, limit: limit, snippetLimit: 0).map(\.session)
    }

    /// One ⌘K result: the session plus its best matching context, with the
    /// CLI's STX/ETX highlight delimiters intact.
    public struct SnippetHit: Equatable, Sendable {
        public let session: IndexedSession
        public let snippet: String?
    }

    /// The CLI's session search, ported faithfully (session_tui_search.go,
    /// store.go): a syntax-error-proof MATCH built by ftsQuery, results
    /// ordered by recency (deliberately not bm25: transcripts make it noisy),
    /// and snippet(...) context fetched in a second query for the head of the
    /// result list.
    /// Lens search: the caller supplies a prebuilt FTS5 MATCH expression
    /// (curated lens queries use OR, which the safe user grammar never
    /// emits). Only pass app-authored expressions here.
    public func searchWithSnippets(matchExpression: String, limit: Int, snippetLimit: Int = 30) throws -> [SnippetHit] {
        guard hasCoreColumns, hasFTS else { return [] }
        let sessions = (try? matchSessions(query: matchExpression, limit: limit)) ?? []
        guard snippetLimit > 0, !sessions.isEmpty else {
            return sessions.map { SnippetHit(session: $0, snippet: nil) }
        }
        let snippets = (try? snippetMap(match: matchExpression, sessions: Array(sessions.prefix(snippetLimit)))) ?? [:]
        return sessions.map { session in
            SnippetHit(session: session, snippet: snippets[session.provider + "\u{0}" + session.sessionID])
        }
    }

    public func searchWithSnippets(_ text: String, limit: Int, snippetLimit: Int = 30) throws -> [SnippetHit] {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, hasCoreColumns else { return [] }
        guard hasFTS, let match = Self.ftsQuery(from: trimmed) else {
            // No FTS table, or under the CLI's two-alphanumeric minimum:
            // LIKE keeps short queries useful rather than silently empty.
            return try likeSessions(text: trimmed, limit: limit).map { SnippetHit(session: $0, snippet: nil) }
        }
        let sessions = try matchSessions(query: match, limit: limit)
        guard snippetLimit > 0, !sessions.isEmpty else {
            return sessions.map { SnippetHit(session: $0, snippet: nil) }
        }
        let snippets = try snippetMap(match: match, sessions: Array(sessions.prefix(snippetLimit)))
        return sessions.map { session in
            SnippetHit(session: session, snippet: snippets[session.provider + "\u{0}" + session.sessionID])
        }
    }

    /// Builds the FTS5 MATCH expression the way the CLI does
    /// (session_tui_search.go:54-79): tokens mirror the unicode61 tokenizer
    /// (runs of letters/digits), so the expression can never syntax-error.
    /// Bare words become prefix terms, punctuated words adjacency chains with
    /// a prefixed last token, closed quotes exact phrases, an unclosed quote
    /// a prefix chain. Returns nil under two alphanumeric characters (the
    /// CLI's minimum query).
    public static func ftsQuery(from raw: String) -> String? {
        guard raw.unicodeScalars.filter({ $0.properties.isAlphabetic || ($0.value >= 48 && $0.value <= 57) }).count >= 2 else {
            return nil
        }

        func tokens(_ text: Substring) -> [String] {
            var result = [String]()
            var current = ""
            for character in text {
                if character.isLetter || character.isNumber {
                    current.append(character)
                } else if !current.isEmpty {
                    result.append(current)
                    current = ""
                }
            }
            if !current.isEmpty { result.append(current) }
            return result
        }

        var parts = [String]()
        var remainder = Substring(raw)
        while let quote = remainder.firstIndex(of: "\"") {
            // Bare words before the quote.
            for word in remainder[..<quote].split(whereSeparator: { $0.isWhitespace }) {
                if let part = Self.wordPart(tokens(word)) { parts.append(part) }
            }
            let afterOpen = remainder.index(after: quote)
            if let close = remainder[afterOpen...].firstIndex(of: "\"") {
                let phraseTokens = tokens(remainder[afterOpen..<close])
                if !phraseTokens.isEmpty {
                    parts.append("\"\(phraseTokens.joined(separator: " "))\"")
                }
                remainder = remainder[remainder.index(after: close)...]
            } else {
                // Unclosed quote: prefix chain over what follows.
                let chain = tokens(remainder[afterOpen...])
                if let part = Self.chainPart(chain) { parts.append(part) }
                remainder = Substring("")
            }
        }
        for word in remainder.split(whereSeparator: { $0.isWhitespace }) {
            if let part = Self.wordPart(tokens(word)) { parts.append(part) }
        }
        return parts.isEmpty ? nil : parts.joined(separator: " ")
    }

    private static func wordPart(_ tokens: [String]) -> String? {
        guard !tokens.isEmpty else { return nil }
        guard tokens.count > 1 else { return tokens[0] + "*" }
        return chainPart(tokens)
    }

    private static func chainPart(_ tokens: [String]) -> String? {
        guard !tokens.isEmpty else { return nil }
        var chain = tokens
        let last = chain.removeLast() + "*"
        return (chain + [last]).joined(separator: " + ")
    }

    /// The CLI's snippet query (store.go:878-880): body is FTS column 3,
    /// char(2)/char(3) mark highlights, 12 tokens of context.
    private func snippetMap(match: String, sessions: [IndexedSession]) throws -> [String: String] {
        guard !sessions.isEmpty else { return [:] }
        let pairClause = sessions
            .map { _ in "(sessions_fts.agent = ? AND sessions_fts.session_id = ?)" }
            .joined(separator: " OR ")
        let sql = """
        SELECT sessions_fts.agent, sessions_fts.session_id,
               snippet(sessions_fts, 3, char(2), char(3), '…', 12)
        FROM sessions_fts
        WHERE sessions_fts MATCH ? AND (\(pairClause))
        """
        let statement = try prepare(sql)
        defer { sqlite3_finalize(statement) }
        sqlite3_bind_text(statement, 1, match, -1, sqliteTransient)
        var index: Int32 = 2
        for session in sessions {
            sqlite3_bind_text(statement, index, session.provider, -1, sqliteTransient)
            sqlite3_bind_text(statement, index + 1, session.sessionID, -1, sqliteTransient)
            index += 2
        }
        var map = [String: String]()
        while true {
            let rc = sqlite3_step(statement)
            if rc == SQLITE_ROW {
                guard let agent = sqlite3_column_text(statement, 0),
                      let sessionID = sqlite3_column_text(statement, 1),
                      let snippet = sqlite3_column_text(statement, 2) else { continue }
                map[String(cString: agent) + "\u{0}" + String(cString: sessionID)] = String(cString: snippet)
            } else if rc == SQLITE_DONE {
                break
            } else {
                throw ReaderError.sqlite(String(cString: sqlite3_errmsg(db)))
            }
        }
        return map
    }

    /// Distinct project directories, most recently active first.
    public func distinctProjectPaths(limit: Int) throws -> [String] {
        guard presentColumns.contains("origin_cwd") else { return [] }
        let sql = """
        SELECT origin_cwd FROM sessions
        WHERE origin_cwd IS NOT NULL AND origin_cwd <> '' AND \(notDeletedClause(prefix: ""))
        GROUP BY origin_cwd
        ORDER BY MAX(\(recencyExpression(prefix: ""))) DESC
        LIMIT ?
        """
        let statement = try prepare(sql)
        defer { sqlite3_finalize(statement) }
        sqlite3_bind_int64(statement, 1, Int64(limit))
        var paths: [String] = []
        while true {
            let rc = sqlite3_step(statement)
            if rc == SQLITE_ROW {
                if let text = sqlite3_column_text(statement, 0) {
                    paths.append(TildeExpansion.expand(String(cString: text)))
                }
            } else if rc == SQLITE_DONE {
                break
            } else {
                throw ReaderError.sqlite(String(cString: sqlite3_errmsg(db)))
            }
        }
        return paths
    }

    // MARK: - Query building

    private var hasCoreColumns: Bool {
        presentColumns.contains("session_id") && presentColumns.contains("agent")
    }

    /// Logical field order shared by every SELECT and the row mapper. Absent
    /// columns (schema drift, or markdown_path which v7 lacks) select NULL.
    private static let selectFields = [
        "session_id", "agent", "origin_cwd", "name", "slug",
        "created_at", "updated_at", "user_turns", "markdown_path",
    ]

    private func selectList(prefix: String) -> String {
        Self.selectFields
            .map { presentColumns.contains($0) ? prefix + $0 : "NULL" }
            .joined(separator: ", ")
    }

    private func notDeletedClause(prefix: String) -> String {
        // The soft-delete tombstone column arrived by migration; older files lack it.
        presentColumns.contains("deleted") ? "\(prefix)deleted = 0" : "1"
    }

    private func recencyExpression(prefix: String) -> String {
        // Timestamps are ISO 8601 TEXT, so lexicographic DESC is chronological.
        let present = ["updated_at", "created_at", "indexed_at"].filter { presentColumns.contains($0) }
        guard !present.isEmpty else { return "\(prefix)rowid" }
        guard present.count > 1 else { return prefix + present[0] }
        return "COALESCE(" + present.map { prefix + $0 }.joined(separator: ", ") + ")"
    }

    private func matchSessions(query: String, limit: Int) throws -> [IndexedSession] {
        let sql = """
        SELECT \(selectList(prefix: "s.")) FROM sessions_fts
        JOIN sessions s ON s.agent = sessions_fts.agent AND s.session_id = sessions_fts.session_id
        WHERE sessions_fts MATCH ? AND \(notDeletedClause(prefix: "s."))
        ORDER BY \(recencyExpression(prefix: "s.")) DESC
        LIMIT ?
        """
        return try querySessions(sql: sql, textBindings: [query], limit: limit)
    }

    private func likeSessions(text: String, limit: Int) throws -> [IndexedSession] {
        let pattern = "%" + Self.escapeForLike(text) + "%"
        let sql: String
        let bindings: [String]
        if hasFTS {
            // FTS5 tables allow plain column reads; a full scan is acceptable
            // for the fallback path and searches the whole conversation body.
            sql = """
            SELECT \(selectList(prefix: "s.")) FROM sessions_fts
            JOIN sessions s ON s.agent = sessions_fts.agent AND s.session_id = sessions_fts.session_id
            WHERE (sessions_fts.body LIKE ? ESCAPE '\\' OR sessions_fts.name LIKE ? ESCAPE '\\')
              AND \(notDeletedClause(prefix: "s."))
            ORDER BY \(recencyExpression(prefix: "s.")) DESC
            LIMIT ?
            """
            bindings = [pattern, pattern]
        } else {
            let nameExpr = presentColumns.contains("name") ? "name" : "''"
            let slugExpr = presentColumns.contains("slug") ? "slug" : "''"
            sql = """
            SELECT \(selectList(prefix: "")) FROM sessions
            WHERE (COALESCE(\(nameExpr), '') LIKE ? ESCAPE '\\' OR COALESCE(\(slugExpr), '') LIKE ? ESCAPE '\\')
              AND \(notDeletedClause(prefix: ""))
            ORDER BY \(recencyExpression(prefix: "")) DESC
            LIMIT ?
            """
            bindings = [pattern, pattern]
        }
        return try querySessions(sql: sql, textBindings: bindings, limit: limit)
    }

    private static func escapeForLike(_ text: String) -> String {
        text
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "%", with: "\\%")
            .replacingOccurrences(of: "_", with: "\\_")
    }

    // MARK: - Execution

    private func prepare(_ sql: String) throws -> OpaquePointer {
        var statement: OpaquePointer?
        guard sqlite3_prepare_v2(db, sql, -1, &statement, nil) == SQLITE_OK, let statement else {
            let message = String(cString: sqlite3_errmsg(db))
            sqlite3_finalize(statement)
            throw ReaderError.sqlite(message)
        }
        return statement
    }

    /// Runs a SELECT whose parameters are `textBindings` followed by the LIMIT.
    private func querySessions(sql: String, textBindings: [String], limit: Int) throws -> [IndexedSession] {
        let statement = try prepare(sql)
        defer { sqlite3_finalize(statement) }
        for (index, text) in textBindings.enumerated() {
            sqlite3_bind_text(statement, Int32(index + 1), text, -1, sqliteTransient)
        }
        sqlite3_bind_int64(statement, Int32(textBindings.count + 1), Int64(limit))

        var sessions: [IndexedSession] = []
        while true {
            let rc = sqlite3_step(statement)
            if rc == SQLITE_ROW {
                if let session = Self.mapRow(statement) {
                    sessions.append(session)
                }
            } else if rc == SQLITE_DONE {
                break
            } else {
                // FTS5 query syntax errors surface here (the text is a bound
                // parameter, so prepare succeeds and step fails).
                throw ReaderError.sqlite(String(cString: sqlite3_errmsg(db)))
            }
        }
        return sessions
    }

    /// Column indexes follow `selectFields`.
    private static func mapRow(_ statement: OpaquePointer) -> IndexedSession? {
        guard let sessionID = textColumn(statement, 0), !sessionID.isEmpty,
              let provider = textColumn(statement, 1) else {
            return nil
        }
        let projectPath = TildeExpansion.expand(textColumn(statement, 2) ?? "")
        let name = textColumn(statement, 3)
        let slug = textColumn(statement, 4)
        let title = firstNonEmpty(name, slug) ?? sessionID
        return IndexedSession(
            sessionID: sessionID,
            provider: provider,
            projectPath: projectPath,
            title: title,
            slug: slug,
            createdAt: parseISODate(textColumn(statement, 5)),
            updatedAt: parseISODate(textColumn(statement, 6)),
            userPromptCount: intColumn(statement, 7),
            markdownPath: textColumn(statement, 8)
        )
    }

    private static func textColumn(_ statement: OpaquePointer, _ index: Int32) -> String? {
        guard sqlite3_column_type(statement, index) != SQLITE_NULL,
              let text = sqlite3_column_text(statement, index) else {
            return nil
        }
        return String(cString: text)
    }

    private static func intColumn(_ statement: OpaquePointer, _ index: Int32) -> Int? {
        guard sqlite3_column_type(statement, index) != SQLITE_NULL else { return nil }
        return Int(sqlite3_column_int64(statement, index))
    }

    private static func firstNonEmpty(_ candidates: String?...) -> String? {
        for candidate in candidates {
            if let candidate, !candidate.isEmpty { return candidate }
        }
        return nil
    }

    // MARK: - Timestamps

    // The CLI writes ISO 8601 (RFC 3339); some providers include fractional seconds.
    private static let isoPlain = ISO8601DateFormatter()
    private static let isoFractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static func parseISODate(_ string: String?) -> Date? {
        guard let string, !string.isEmpty else { return nil }
        return isoPlain.date(from: string) ?? isoFractional.date(from: string)
    }

    // MARK: - Schema probing

    private static func columnNames(db: OpaquePointer, table: String) -> Set<String> {
        var statement: OpaquePointer?
        guard sqlite3_prepare_v2(db, "PRAGMA table_info(\(table))", -1, &statement, nil) == SQLITE_OK,
              let statement else {
            return []
        }
        defer { sqlite3_finalize(statement) }
        var names = Set<String>()
        while sqlite3_step(statement) == SQLITE_ROW {
            if let text = sqlite3_column_text(statement, 1) {
                names.insert(String(cString: text))
            }
        }
        return names
    }

    private static func tableExists(db: OpaquePointer, name: String) -> Bool {
        var statement: OpaquePointer?
        let sql = "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?"
        guard sqlite3_prepare_v2(db, sql, -1, &statement, nil) == SQLITE_OK, let statement else {
            return false
        }
        defer { sqlite3_finalize(statement) }
        sqlite3_bind_text(statement, 1, name, -1, sqliteTransient)
        return sqlite3_step(statement) == SQLITE_ROW
    }
}

/// Shared helper: expand a leading `~` using the real home directory (file
/// APIs never expand literal tildes).
enum TildeExpansion {
    static func expand(_ path: String) -> String {
        guard path == "~" || path.hasPrefix("~/") else { return path }
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        return path == "~" ? home : home + String(path.dropFirst(1))
    }
}
