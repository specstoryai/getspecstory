import Foundation
import SQLite3

// sqlite3_bind_text needs SQLITE_TRANSIENT so SQLite copies Swift-owned UTF-8
// buffers before they are deallocated; the C macro does not import into Swift.
private let sqliteTransient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

/// Aggregate analytics over `~/.specstory/sessions.db`, the CLI-owned
/// rebuildable session index (schema v7). Opens its own read-only connection
/// (same discipline as SessionIndexReader: SQLITE_OPEN_READONLY, 2 s busy
/// timeout, tiny page cache, tolerate a missing file, filter `deleted = 0`)
/// so dashboard queries never contend with the feed reader.
///
/// Everything here is pure SQL aggregation (GROUP BY / COUNT / SUM); no
/// session rows are scanned in Swift. Cloud parity: these queries mirror the
/// cloud AnalyticsOverviewSchema locally. Day buckets use `date(created_at)`
/// (UTC, like the cloud's UTC bucketing on COALESCE(started_at, created_at));
/// the hour histogram converts to the Mac's local time, matching the cloud
/// punchcard's caller-timezone behavior; the streak walk ports the cloud
/// buildAnalyticsOverview activity logic.
public final class AnalyticsQueries {
    public enum ReaderError: Error {
        case databaseMissing
        case sqlite(String)
    }

    private let db: OpaquePointer
    /// Columns actually present in `sessions`, probed at init for drift
    /// tolerance: queries touching an absent column return empty results.
    private let presentColumns: Set<String>

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
        // Aggregations here are shallow, so keep the cache tiny (2 MB).
        sqlite3_exec(opened, "PRAGMA cache_size = -2000", nil, nil, nil)
        db = opened
        presentColumns = Self.columnNames(db: opened, table: "sessions")
    }

    deinit {
        sqlite3_close(db)
    }

    // MARK: - Queries

    /// Session counts per UTC day over the trailing window, oldest first.
    /// Days with no sessions are absent (callers zero-fill for charting).
    /// Cloud parity: sessionsByDayByAgent collapsed across agents.
    public func sessionsPerDay(days: Int, now: Date = Date()) throws -> [(day: String, count: Int)] {
        guard days > 0, has("created_at") else { return [] }
        let cutoff = Self.utcDayString(for: now.addingTimeInterval(-Double(days - 1) * 86_400))
        let sql = """
        SELECT date(created_at) AS day, COUNT(*)
        FROM sessions
        WHERE \(notDeletedClause) AND date(created_at) IS NOT NULL AND date(created_at) >= ?
        GROUP BY day
        ORDER BY day ASC
        """
        return try query(sql, bind: { statement in
            sqlite3_bind_text(statement, 1, cutoff, -1, sqliteTransient)
        }, row: { statement in
            guard let day = Self.textColumn(statement, 0) else { return nil }
            return (day: day, count: Int(sqlite3_column_int64(statement, 1)))
        })
    }

    /// Sessions and user prompts per agent, most-used agent first.
    /// Cloud parity: agentTotals.byAgent plus the agents[] exchange counts.
    public func providerBreakdown() throws -> [(provider: String, sessions: Int, userTurns: Int)] {
        guard has("agent") else { return [] }
        let sql = """
        SELECT agent, COUNT(*) AS sessions, \(sumExpression("user_turns"))
        FROM sessions
        WHERE \(notDeletedClause)
        GROUP BY agent
        ORDER BY sessions DESC, agent ASC
        """
        return try query(sql, row: { statement in
            guard let provider = Self.textColumn(statement, 0) else { return nil }
            return (
                provider: provider,
                sessions: Int(sqlite3_column_int64(statement, 1)),
                userTurns: Int(sqlite3_column_int64(statement, 2))
            )
        })
    }

    /// Per-project totals ordered by session count. `lastActivity` is the
    /// newest `updated_at` (falling back to `created_at`) in the project.
    /// Cloud parity: topProjects[] (sessionCount, lastActiveIso).
    public func projectTotals(limit: Int) throws -> [(projectID: String, projectName: String, sessions: Int, lastActivity: Date?, userTurns: Int)] {
        guard has("project_id") else { return [] }
        let nameExpr = has("project_name") ? "MAX(COALESCE(project_name, ''))" : "''"
        let activityParts = ["updated_at", "created_at"].filter { has($0) }
        let activityExpr = activityParts.isEmpty
            ? "NULL"
            : "MAX(COALESCE(" + activityParts.joined(separator: ", ") + "))"
        let sql = """
        SELECT project_id, \(nameExpr) AS name, COUNT(*) AS sessions,
               \(activityExpr) AS last_activity, \(sumExpression("user_turns"))
        FROM sessions
        WHERE \(notDeletedClause)
        GROUP BY project_id
        ORDER BY sessions DESC, last_activity DESC
        LIMIT ?
        """
        return try query(sql, bind: { statement in
            sqlite3_bind_int64(statement, 1, Int64(limit))
        }, row: { statement in
            guard let projectID = Self.textColumn(statement, 0) else { return nil }
            return (
                projectID: projectID,
                projectName: Self.textColumn(statement, 1) ?? "",
                sessions: Int(sqlite3_column_int64(statement, 2)),
                lastActivity: Self.parseISODate(Self.textColumn(statement, 3)),
                userTurns: Int(sqlite3_column_int64(statement, 4))
            )
        })
    }

    /// Headline totals across the whole index. `agentTurns` is derived
    /// (total_turns counts user + agent messages). Cloud parity: meta.coverage
    /// totals plus messages.totalExchanges.
    public func totals() throws -> (sessions: Int, projects: Int, userTurns: Int, agentTurns: Int) {
        guard has("session_id") else { return (0, 0, 0, 0) }
        let projectExpr = has("project_id") ? "COUNT(DISTINCT project_id)" : "0"
        let sql = """
        SELECT COUNT(*), \(projectExpr), \(sumExpression("user_turns")), \(sumExpression("total_turns"))
        FROM sessions
        WHERE \(notDeletedClause)
        """
        let rows = try query(sql, row: { statement in
            (
                sessions: Int(sqlite3_column_int64(statement, 0)),
                projects: Int(sqlite3_column_int64(statement, 1)),
                userTurns: Int(sqlite3_column_int64(statement, 2)),
                totalTurns: Int(sqlite3_column_int64(statement, 3))
            )
        })
        guard let row = rows.first else { return (0, 0, 0, 0) }
        return (
            sessions: row.sessions,
            projects: row.projects,
            userTurns: row.userTurns,
            agentTurns: max(0, row.totalTurns - row.userTurns)
        )
    }

    /// Distinct UTC days with at least one session, newest first.
    public func distinctActiveDays(limit: Int) throws -> [String] {
        guard has("created_at") else { return [] }
        let sql = """
        SELECT DISTINCT date(created_at) AS day
        FROM sessions
        WHERE \(notDeletedClause) AND date(created_at) IS NOT NULL
        ORDER BY day DESC
        LIMIT ?
        """
        return try query(sql, bind: { statement in
            sqlite3_bind_int64(statement, 1, Int64(limit))
        }, row: { statement in
            Self.textColumn(statement, 0)
        })
    }

    /// Consecutive-day streak ending today or yesterday (an idle today does
    /// not break yesterday's streak). Cloud parity: activity.currentStreak in
    /// buildAnalyticsOverview. Ten years of distinct days bounds the walk.
    public func currentStreak(calendar: Calendar = .current, now: Date = Date()) throws -> Int {
        let activeDays = try distinctActiveDays(limit: 3660)
        return Self.streak(activeDays: activeDays, calendar: calendar, now: now)
    }

    /// Pure streak math over "yyyy-MM-dd" day strings; exposed for testing
    /// with a fixed clock.
    public static func streak(activeDays: [String], calendar: Calendar, now: Date) -> Int {
        guard !activeDays.isEmpty else { return 0 }
        let active = Set(activeDays)
        let formatter = dayFormatter(calendar: calendar)
        var cursor = calendar.startOfDay(for: now)
        if !active.contains(formatter.string(from: cursor)) {
            guard let yesterday = calendar.date(byAdding: .day, value: -1, to: cursor),
                  active.contains(formatter.string(from: yesterday)) else {
                return 0
            }
            cursor = yesterday
        }
        var count = 0
        while active.contains(formatter.string(from: cursor)) {
            count += 1
            guard let previous = calendar.date(byAdding: .day, value: -1, to: cursor) else { break }
            cursor = previous
        }
        return count
    }

    /// Session starts per hour of day, 24 buckets (0 = midnight), converted
    /// to the Mac's local time. Cloud parity: the punchcard collapsed to
    /// hours, which is likewise rendered in the caller's timezone.
    public func hourHistogram() throws -> [Int] {
        var buckets = [Int](repeating: 0, count: 24)
        guard has("created_at") else { return buckets }
        let sql = """
        SELECT CAST(strftime('%H', created_at, 'localtime') AS INTEGER) AS hour, COUNT(*)
        FROM sessions
        WHERE \(notDeletedClause) AND strftime('%H', created_at, 'localtime') IS NOT NULL
        GROUP BY hour
        """
        let rows = try query(sql, row: { statement in
            (hour: Int(sqlite3_column_int64(statement, 0)), count: Int(sqlite3_column_int64(statement, 1)))
        })
        for row in rows where (0..<24).contains(row.hour) {
            buckets[row.hour] = row.count
        }
        return buckets
    }

    // MARK: - Query building

    private func has(_ column: String) -> Bool {
        presentColumns.contains(column)
    }

    private var notDeletedClause: String {
        // The soft-delete tombstone column arrived by migration; older files lack it.
        has("deleted") ? "deleted = 0" : "1"
    }

    /// SUM over an optional column; 0 when the column is absent or all NULL.
    private func sumExpression(_ column: String) -> String {
        has(column) ? "COALESCE(SUM(\(column)), 0)" : "0"
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

    private func query<T>(
        _ sql: String,
        bind: (OpaquePointer) -> Void = { _ in },
        row: (OpaquePointer) -> T?
    ) throws -> [T] {
        let statement = try prepare(sql)
        defer { sqlite3_finalize(statement) }
        bind(statement)
        var results: [T] = []
        while true {
            let rc = sqlite3_step(statement)
            if rc == SQLITE_ROW {
                if let value = row(statement) {
                    results.append(value)
                }
            } else if rc == SQLITE_DONE {
                break
            } else {
                throw ReaderError.sqlite(String(cString: sqlite3_errmsg(db)))
            }
        }
        return results
    }

    private static func textColumn(_ statement: OpaquePointer, _ index: Int32) -> String? {
        guard sqlite3_column_type(statement, index) != SQLITE_NULL,
              let text = sqlite3_column_text(statement, index) else {
            return nil
        }
        return String(cString: text)
    }

    // MARK: - Dates

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

    /// SQLite's date() buckets in UTC; window cutoffs must match.
    private static func utcDayString(for date: Date) -> String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC") ?? .current
        return dayFormatter(calendar: calendar).string(from: date)
    }

    private static func dayFormatter(calendar: Calendar) -> DateFormatter {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.calendar = calendar
        formatter.timeZone = calendar.timeZone
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
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
}
