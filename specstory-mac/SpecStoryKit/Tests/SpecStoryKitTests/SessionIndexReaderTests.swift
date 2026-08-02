import SQLite3
import XCTest
@testable import SpecStoryKit

final class SessionIndexReaderTests: XCTestCase {
    private var tempDir: URL!
    private var dbURL: URL!

    override func setUpWithError() throws {
        tempDir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("SessionIndexReaderTests-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
        dbURL = tempDir.appendingPathComponent("sessions.db")
        try makeFixtureDatabase(at: dbURL)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: tempDir)
    }

    // MARK: - Fixture

    /// Builds a sessions.db matching the CLI's v7 DDL (pkg/sessionindex/store.go):
    /// four live rows across two projects and two providers, plus one
    /// soft-deleted row, with matching FTS bodies.
    private func makeFixtureDatabase(at url: URL) throws {
        var handle: OpaquePointer?
        XCTAssertEqual(sqlite3_open(url.path, &handle), SQLITE_OK)
        guard let db = handle else {
            XCTFail("could not open fixture database")
            return
        }
        defer { sqlite3_close(db) }

        exec(db, "PRAGMA journal_mode=WAL")
        exec(db, """
        CREATE TABLE IF NOT EXISTS sessions (
            project_id   TEXT NOT NULL,
            project_name TEXT,
            agent        TEXT NOT NULL,
            session_id   TEXT NOT NULL,
            created_at   TEXT,
            updated_at   TEXT,
            user_turns   INTEGER,
            total_turns  INTEGER,
            slug         TEXT,
            name         TEXT,
            native_path  TEXT,
            origin_cwd   TEXT,
            size          INTEGER,
            mtime         INTEGER,
            index_version INTEGER,
            indexed_at    TEXT,
            fts_rowid     INTEGER,
            deleted       INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY (agent, session_id)
        )
        """)
        exec(db, "CREATE INDEX IF NOT EXISTS idx_sessions_project_recent ON sessions(project_id, updated_at, created_at)")
        exec(db, """
        CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5(
            session_id UNINDEXED,
            agent UNINDEXED,
            name,
            body
        )
        """)

        struct Row {
            let projectID: String
            let agent: String
            let sessionID: String
            let createdAt: String
            let updatedAt: String
            let userTurns: Int
            let slug: String
            let name: String
            let originCwd: String
            let body: String
            let deleted: Int
        }
        let rows: [Row] = [
            Row(projectID: "proj-a", agent: "claude", sessionID: "s1",
                createdAt: "2025-01-01T10:00:00Z", updatedAt: "2025-01-04T10:00:00Z",
                userTurns: 3, slug: "fix-login-bug", name: "Fix login bug",
                originCwd: "/tmp/projA",
                body: "we fixed the alpha login flow with func main() { }", deleted: 0),
            Row(projectID: "proj-a", agent: "codex", sessionID: "s2",
                createdAt: "2025-01-02T09:00:00Z", updatedAt: "2025-01-03T09:00:00Z",
                userTurns: 1, slug: "beta-renderer", name: "Beta renderer",
                originCwd: "/tmp/projA",
                body: "beta renderer refactor work", deleted: 0),
            Row(projectID: "proj-b", agent: "claude", sessionID: "s3",
                createdAt: "2025-01-05T08:00:00Z", updatedAt: "2025-01-05T08:30:00Z",
                userTurns: 7, slug: "alpha-zebra", name: "Alpha zebra hunt",
                originCwd: "/tmp/projB",
                body: "alpha searching for the zebra", deleted: 0),
            Row(projectID: "proj-b", agent: "codex", sessionID: "s4",
                createdAt: "2025-01-02T07:00:00Z", updatedAt: "2025-01-02T07:45:00Z",
                userTurns: 2, slug: "gamma-content", name: "Gamma content",
                originCwd: "/tmp/projB",
                body: "gamma content calling main() { return 0 }", deleted: 0),
            Row(projectID: "proj-b", agent: "claude", sessionID: "s5-deleted",
                createdAt: "2025-01-06T07:00:00Z", updatedAt: "2025-01-06T07:45:00Z",
                userTurns: 1, slug: "tombstoned", name: "Tombstoned session",
                originCwd: "/tmp/projB",
                body: "alpha content that was soft deleted", deleted: 1),
        ]
        for row in rows {
            exec(db, """
            INSERT INTO sessions (project_id, project_name, agent, session_id, created_at, updated_at,
                user_turns, total_turns, slug, name, native_path, origin_cwd,
                size, mtime, index_version, indexed_at, fts_rowid, deleted)
            VALUES ('\(row.projectID)', '\(row.projectID)', '\(row.agent)', '\(row.sessionID)',
                '\(row.createdAt)', '\(row.updatedAt)', \(row.userTurns), \(row.userTurns * 2),
                '\(row.slug)', '\(row.name)', '/native/\(row.sessionID)', '\(row.originCwd)',
                100, 1700000000000, 7, '\(row.updatedAt)', NULL, \(row.deleted))
            """)
            // Real tombstones keep the row but drop the FTS body; mirror that.
            if row.deleted == 0 {
                exec(db, """
                INSERT INTO sessions_fts (session_id, agent, name, body)
                VALUES ('\(row.sessionID)', '\(row.agent)', '\(row.name)', '\(row.body)')
                """)
            }
        }
        // Checkpoint so the read-only connection sees everything without WAL recovery.
        exec(db, "PRAGMA wal_checkpoint(TRUNCATE)")
    }

    private func exec(_ db: OpaquePointer, _ sql: String) {
        var errorMessage: UnsafeMutablePointer<CChar>?
        let rc = sqlite3_exec(db, sql, nil, nil, &errorMessage)
        if rc != SQLITE_OK {
            let message = errorMessage.map { String(cString: $0) } ?? "unknown"
            sqlite3_free(errorMessage)
            XCTFail("fixture SQL failed (\(rc)): \(message)\n\(sql)")
        }
    }

    // MARK: - Tests

    func testMissingDatabaseThrowsDatabaseMissing() {
        let missing = tempDir.appendingPathComponent("does-not-exist.db")
        XCTAssertThrowsError(try SessionIndexReader(databaseURL: missing)) { error in
            guard case SessionIndexReader.ReaderError.databaseMissing = error else {
                XCTFail("expected databaseMissing, got \(error)")
                return
            }
        }
    }

    func testRecentSessionsOrdersMostRecentFirstAndSkipsDeleted() throws {
        let reader = try SessionIndexReader(databaseURL: dbURL)
        let sessions = try reader.recentSessions(limit: 10)

        XCTAssertEqual(sessions.map(\.sessionID), ["s3", "s1", "s2", "s4"])
        XCTAssertFalse(sessions.contains { $0.sessionID == "s5-deleted" })

        let first = sessions[0]
        XCTAssertEqual(first.provider, "claude")
        XCTAssertEqual(first.projectPath, "/tmp/projB")
        XCTAssertEqual(first.title, "Alpha zebra hunt")
        XCTAssertEqual(first.slug, "alpha-zebra")
        XCTAssertEqual(first.userPromptCount, 7)
        XCTAssertNil(first.markdownPath)
        XCTAssertNotNil(first.createdAt)
        XCTAssertNotNil(first.updatedAt)
        if let createdAt = first.createdAt, let updatedAt = first.updatedAt {
            XCTAssertLessThan(createdAt, updatedAt)
        }
    }

    func testRecentSessionsHonorsLimit() throws {
        let reader = try SessionIndexReader(databaseURL: dbURL)
        let sessions = try reader.recentSessions(limit: 2)
        XCTAssertEqual(sessions.map(\.sessionID), ["s3", "s1"])
    }

    func testSearchFindsFTSMatchesNewestFirst() throws {
        let reader = try SessionIndexReader(databaseURL: dbURL)
        let hits = try reader.search("alpha", limit: 10)
        // s3 (2025-01-05) before s1 (2025-01-04); the deleted row's body is
        // not in the FTS table, so it cannot match.
        XCTAssertEqual(hits.map(\.sessionID), ["s3", "s1"])
    }

    func testSearchFallsBackToLikeOnMalformedFTSQuery() throws {
        let reader = try SessionIndexReader(databaseURL: dbURL)
        // "main() {" is an FTS5 syntax error (unbalanced parens/brace), so the
        // reader must fall back to a LIKE substring scan over the bodies.
        let hits = try reader.search("main() {", limit: 10)
        XCTAssertEqual(hits.map(\.sessionID), ["s1", "s4"])
    }

    func testSearchEmptyTextReturnsEmpty() throws {
        let reader = try SessionIndexReader(databaseURL: dbURL)
        XCTAssertEqual(try reader.search("   ", limit: 10), [])
    }

    func testDistinctProjectPathsOrderedByRecency() throws {
        let reader = try SessionIndexReader(databaseURL: dbURL)
        let paths = try reader.distinctProjectPaths(limit: 10)
        // projB's newest live activity is 2025-01-05 (s3), projA's is 2025-01-04 (s1).
        XCTAssertEqual(paths, ["/tmp/projB", "/tmp/projA"])
    }

    func testDefaultDatabaseURLUsesRealHome() {
        let url = SessionIndexReader.defaultDatabaseURL
        XCTAssertFalse(url.path.contains("~"))
        XCTAssertTrue(url.path.hasSuffix("/.specstory/sessions.db"))
    }

    // MARK: - Crashed-writer WAL recovery

    func testRecoversHotWALFromCrashedWriter() throws {
        // Reopen the fixture read-write and add a row so the WAL holds
        // uncheckpointed frames again.
        var handle: OpaquePointer?
        XCTAssertEqual(sqlite3_open(dbURL.path, &handle), SQLITE_OK)
        let db = try XCTUnwrap(handle)
        exec(db, "PRAGMA journal_mode=WAL")
        exec(db, """
        INSERT INTO sessions (project_id, project_name, agent, session_id, created_at, updated_at,
            user_turns, total_turns, slug, name, native_path, origin_cwd,
            size, mtime, index_version, indexed_at, fts_rowid, deleted)
        VALUES ('proj-c', 'proj-c', 'claude', 's6-crashed',
            '2025-01-07T07:00:00Z', '2025-01-07T07:45:00Z', 1, 2,
            'crashed', 'Crashed writer row', '/native/s6', '/tmp/projC',
            100, 1700000000000, 7, '2025-01-07T07:45:00Z', NULL, 0)
        """)

        // Copy the database with its hot WAL while the writer still holds
        // it, mimicking a writer killed mid-flight: the copy's WAL index was
        // never cleanly shut down, which a readonly connection cannot
        // recover on its own (SQLITE_READONLY_RECOVERY).
        let crashed = tempDir.appendingPathComponent("crashed.db")
        for ext in ["", "-wal", "-shm"] {
            let source = URL(fileURLWithPath: dbURL.path + ext)
            if FileManager.default.fileExists(atPath: source.path) {
                try FileManager.default.copyItem(
                    at: source, to: URL(fileURLWithPath: crashed.path + ext))
            }
        }
        sqlite3_close(db)

        let reader = try SessionIndexReader(databaseURL: crashed)
        XCTAssertEqual(try reader.count(), 5)
        XCTAssertTrue(try reader.recentSessions(limit: 10).contains { $0.sessionID == "s6-crashed" })
    }
}
