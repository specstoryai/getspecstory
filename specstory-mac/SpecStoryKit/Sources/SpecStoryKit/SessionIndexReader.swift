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
/// Invariants: opened with SQLITE_OPEN_READONLY (never writes, never creates),
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
        db = opened
        presentColumns = Self.columnNames(db: opened, table: "sessions")
        hasFTS = Self.tableExists(db: opened, name: "sessions_fts")
    }

    deinit {
        sqlite3_close(db)
    }

    // MARK: - Public queries

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
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, hasCoreColumns else { return [] }
        if hasFTS {
            do {
                return try matchSessions(query: trimmed, limit: limit)
            } catch {
                // Fall through to LIKE on FTS syntax errors (surfaced at step time).
            }
        }
        return try likeSessions(text: trimmed, limit: limit)
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
