import SQLite3
import XCTest
@testable import SpecStoryKit

final class AnalyticsQueriesTests: XCTestCase {
    private var tempDir: URL!
    private var dbURL: URL!

    /// Fixed clock inside the fixture window: 2025-03-12 15:00 UTC.
    private let fixedNow = ISO8601DateFormatter().date(from: "2025-03-12T15:00:00Z")!

    /// UTC calendar so streak day-walking matches SQLite's UTC date() buckets
    /// deterministically, independent of the machine running the tests.
    private var utcCalendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        return calendar
    }()

    override func setUpWithError() throws {
        tempDir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("AnalyticsQueriesTests-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
        dbURL = tempDir.appendingPathComponent("sessions.db")
        try makeFixtureDatabase(at: dbURL)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: tempDir)
    }

    // MARK: - Fixture

    private struct Row {
        let projectID: String
        let projectName: String
        let agent: String
        let sessionID: String
        let createdAt: String
        let updatedAt: String
        let userTurns: Int
        let totalTurns: Int
        let deleted: Int
    }

    /// Ten rows across three UTC days (03-10, 03-11, 03-12), two providers,
    /// two projects, one soft-deleted tombstone.
    private let rows: [Row] = [
        Row(projectID: "proj-a", projectName: "Alpha", agent: "claude", sessionID: "s1",
            createdAt: "2025-03-10T09:15:00Z", updatedAt: "2025-03-10T10:00:00Z",
            userTurns: 3, totalTurns: 8, deleted: 0),
        Row(projectID: "proj-a", projectName: "Alpha", agent: "claude", sessionID: "s2",
            createdAt: "2025-03-10T14:30:00Z", updatedAt: "2025-03-10T15:00:00Z",
            userTurns: 2, totalTurns: 5, deleted: 0),
        Row(projectID: "proj-a", projectName: "Alpha", agent: "codex", sessionID: "s3",
            createdAt: "2025-03-10T18:45:00Z", updatedAt: "2025-03-10T19:30:00Z",
            userTurns: 4, totalTurns: 9, deleted: 0),
        Row(projectID: "proj-b", projectName: "Beta", agent: "claude", sessionID: "s4",
            createdAt: "2025-03-11T09:15:00Z", updatedAt: "2025-03-11T09:45:00Z",
            userTurns: 1, totalTurns: 2, deleted: 0),
        Row(projectID: "proj-b", projectName: "Beta", agent: "codex", sessionID: "s5",
            createdAt: "2025-03-11T11:00:00Z", updatedAt: "2025-03-11T12:00:00Z",
            userTurns: 5, totalTurns: 12, deleted: 0),
        Row(projectID: "proj-a", projectName: "Alpha", agent: "claude", sessionID: "s6",
            createdAt: "2025-03-12T08:05:00Z", updatedAt: "2025-03-12T08:30:00Z",
            userTurns: 2, totalTurns: 6, deleted: 0),
        Row(projectID: "proj-b", projectName: "Beta", agent: "codex", sessionID: "s7",
            createdAt: "2025-03-12T09:15:00Z", updatedAt: "2025-03-12T09:45:00Z",
            userTurns: 3, totalTurns: 7, deleted: 0),
        Row(projectID: "proj-a", projectName: "Alpha", agent: "claude", sessionID: "s8",
            createdAt: "2025-03-12T22:10:00Z", updatedAt: "2025-03-12T23:05:00Z",
            userTurns: 4, totalTurns: 10, deleted: 0),
        Row(projectID: "proj-b", projectName: "Beta", agent: "claude", sessionID: "s9",
            createdAt: "2025-03-12T09:40:00Z", updatedAt: "2025-03-12T10:20:00Z",
            userTurns: 1, totalTurns: 3, deleted: 0),
        // Tombstone: every query must ignore it.
        Row(projectID: "proj-a", projectName: "Alpha", agent: "claude", sessionID: "s10-deleted",
            createdAt: "2025-03-12T10:00:00Z", updatedAt: "2025-03-12T11:00:00Z",
            userTurns: 9, totalTurns: 20, deleted: 1),
    ]

    private var liveRows: [Row] { rows.filter { $0.deleted == 0 } }

    /// Builds a sessions.db matching the v7 DDL subset the analytics queries
    /// touch (pkg/sessionindex/store.go).
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
        for row in rows {
            exec(db, """
            INSERT INTO sessions (project_id, project_name, agent, session_id, created_at, updated_at,
                user_turns, total_turns, slug, name, native_path, origin_cwd,
                size, mtime, index_version, indexed_at, fts_rowid, deleted)
            VALUES ('\(row.projectID)', '\(row.projectName)', '\(row.agent)', '\(row.sessionID)',
                '\(row.createdAt)', '\(row.updatedAt)', \(row.userTurns), \(row.totalTurns),
                'slug-\(row.sessionID)', 'Session \(row.sessionID)', '/native/\(row.sessionID)', '/tmp/\(row.projectID)',
                100, 1700000000000, 7, '\(row.updatedAt)', NULL, \(row.deleted))
            """)
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

    private func makeQueries() throws -> AnalyticsQueries {
        try AnalyticsQueries(databaseURL: dbURL)
    }

    // MARK: - Missing database

    func testMissingDatabaseThrowsDatabaseMissing() {
        let missing = tempDir.appendingPathComponent("does-not-exist.db")
        XCTAssertThrowsError(try AnalyticsQueries(databaseURL: missing)) { error in
            guard case AnalyticsQueries.ReaderError.databaseMissing = error else {
                XCTFail("expected databaseMissing, got \(error)")
                return
            }
        }
    }

    // MARK: - Sessions per day

    func testSessionsPerDayCountsUTCDaysAndSkipsDeleted() throws {
        let perDay = try makeQueries().sessionsPerDay(days: 30, now: fixedNow)
        XCTAssertEqual(perDay.map(\.day), ["2025-03-10", "2025-03-11", "2025-03-12"])
        // 03-12 would be 5 if the tombstone leaked in.
        XCTAssertEqual(perDay.map(\.count), [3, 2, 4])
    }

    func testSessionsPerDayHonorsWindow() throws {
        // A 2-day window ending 03-12 starts at 03-11.
        let perDay = try makeQueries().sessionsPerDay(days: 2, now: fixedNow)
        XCTAssertEqual(perDay.map(\.day), ["2025-03-11", "2025-03-12"])
        XCTAssertEqual(perDay.map(\.count), [2, 4])
    }

    // MARK: - Provider breakdown

    func testProviderBreakdownAggregatesSessionsAndPrompts() throws {
        let breakdown = try makeQueries().providerBreakdown()
        XCTAssertEqual(breakdown.count, 2)

        // Most-used agent first. claude: s1 s2 s4 s6 s8 s9; codex: s3 s5 s7.
        XCTAssertEqual(breakdown[0].provider, "claude")
        XCTAssertEqual(breakdown[0].sessions, 6)
        XCTAssertEqual(breakdown[0].userTurns, 13)

        XCTAssertEqual(breakdown[1].provider, "codex")
        XCTAssertEqual(breakdown[1].sessions, 3)
        XCTAssertEqual(breakdown[1].userTurns, 12)
    }

    // MARK: - Project totals

    func testProjectTotalsGroupsAndOrdersBySessions() throws {
        let projects = try makeQueries().projectTotals(limit: 10)
        XCTAssertEqual(projects.map(\.projectID), ["proj-a", "proj-b"])

        let alpha = projects[0]
        XCTAssertEqual(alpha.projectName, "Alpha")
        XCTAssertEqual(alpha.sessions, 5)
        XCTAssertEqual(alpha.userTurns, 15)
        // Newest updated_at in proj-a is s8's 2025-03-12T23:05:00Z.
        XCTAssertEqual(alpha.lastActivity, ISO8601DateFormatter().date(from: "2025-03-12T23:05:00Z"))

        let beta = projects[1]
        XCTAssertEqual(beta.projectName, "Beta")
        XCTAssertEqual(beta.sessions, 4)
        XCTAssertEqual(beta.userTurns, 10)
        XCTAssertEqual(beta.lastActivity, ISO8601DateFormatter().date(from: "2025-03-12T10:20:00Z"))
    }

    func testProjectTotalsHonorsLimit() throws {
        let projects = try makeQueries().projectTotals(limit: 1)
        XCTAssertEqual(projects.map(\.projectID), ["proj-a"])
    }

    // MARK: - Totals

    func testTotalsCountsLiveRowsOnly() throws {
        let totals = try makeQueries().totals()
        XCTAssertEqual(totals.sessions, 9)
        XCTAssertEqual(totals.projects, 2)
        XCTAssertEqual(totals.userTurns, 25)
        // total_turns sums to 62; agent turns are the remainder.
        XCTAssertEqual(totals.agentTurns, 37)
    }

    // MARK: - Active days and streak

    func testDistinctActiveDaysNewestFirst() throws {
        let days = try makeQueries().distinctActiveDays(limit: 10)
        XCTAssertEqual(days, ["2025-03-12", "2025-03-11", "2025-03-10"])
    }

    func testDistinctActiveDaysHonorsLimit() throws {
        let days = try makeQueries().distinctActiveDays(limit: 2)
        XCTAssertEqual(days, ["2025-03-12", "2025-03-11"])
    }

    func testCurrentStreakWithActivityToday() throws {
        let streak = try makeQueries().currentStreak(calendar: utcCalendar, now: fixedNow)
        XCTAssertEqual(streak, 3)
    }

    func testCurrentStreakSurvivesIdleToday() throws {
        // The day after the last active day: yesterday anchors the streak.
        let now = ISO8601DateFormatter().date(from: "2025-03-13T08:00:00Z")!
        let streak = try makeQueries().currentStreak(calendar: utcCalendar, now: now)
        XCTAssertEqual(streak, 3)
    }

    func testCurrentStreakZeroAfterTwoIdleDays() throws {
        let now = ISO8601DateFormatter().date(from: "2025-03-14T08:00:00Z")!
        let streak = try makeQueries().currentStreak(calendar: utcCalendar, now: now)
        XCTAssertEqual(streak, 0)
    }

    func testStreakHelperStopsAtGaps() {
        let streak = AnalyticsQueries.streak(
            activeDays: ["2025-03-12", "2025-03-11", "2025-03-08", "2025-03-07"],
            calendar: utcCalendar,
            now: fixedNow
        )
        XCTAssertEqual(streak, 2)
    }

    func testStreakHelperEmptyIsZero() {
        XCTAssertEqual(AnalyticsQueries.streak(activeDays: [], calendar: utcCalendar, now: fixedNow), 0)
    }

    // MARK: - Hour histogram

    func testHourHistogramBucketsLocalHoursAndSkipsDeleted() throws {
        let histogram = try makeQueries().hourHistogram()
        XCTAssertEqual(histogram.count, 24)
        XCTAssertEqual(histogram.reduce(0, +), 9, "one bucket increment per live row")

        // The query converts with SQLite's 'localtime'; mirror it with the
        // system calendar so the assertion holds in any timezone.
        let iso = ISO8601DateFormatter()
        var expected = [Int](repeating: 0, count: 24)
        for row in liveRows {
            let hour = Calendar.current.component(.hour, from: iso.date(from: row.createdAt)!)
            expected[hour] += 1
        }
        XCTAssertEqual(histogram, expected)
    }
}
