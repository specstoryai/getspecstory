import Foundation
import SwiftUI
import SpecStoryKit

/// One day of the sessions-per-day series, zero-filled across the window.
/// `date` is local midnight of the UTC bucket day (bucketing is UTC for
/// cloud analytics parity; presentation is local).
struct AnalyticsDayCount: Identifiable, Sendable {
    let date: Date
    let count: Int
    var id: Date { date }
}

struct AnalyticsProviderStat: Identifiable, Sendable {
    let provider: String
    let sessions: Int
    let userTurns: Int
    var id: String { provider }
}

struct AnalyticsProjectStat: Identifiable, Sendable {
    let projectID: String
    let name: String
    let sessions: Int
    let lastActivity: Date?
    let userTurns: Int
    var id: String { projectID }
}

/// Immutable result of one analytics load: every dashboard query, taken
/// together off the main actor.
struct AnalyticsSnapshot: Sendable {
    var sessions = 0
    var projects = 0
    var userTurns = 0
    var agentTurns = 0
    var streak = 0
    var perDay: [AnalyticsDayCount] = []
    var providers: [AnalyticsProviderStat] = []
    var topProjects: [AnalyticsProjectStat] = []
    var hourHistogram = [Int](repeating: 0, count: 24)

    static let empty = AnalyticsSnapshot()

    /// Opens a fresh read-only connection and runs every aggregate query.
    /// Blocking SQLite work: call from a detached task, never the main actor.
    /// A missing sessions.db (first launch, mid-reindex) yields `.empty`.
    static func load(days: Int, now: Date = Date()) -> AnalyticsSnapshot {
        guard let queries = try? AnalyticsQueries(databaseURL: AnalyticsQueries.defaultDatabaseURL) else {
            return .empty
        }
        var snapshot = AnalyticsSnapshot()
        if let totals = try? queries.totals() {
            snapshot.sessions = totals.sessions
            snapshot.projects = totals.projects
            snapshot.userTurns = totals.userTurns
            snapshot.agentTurns = totals.agentTurns
        }
        let sparse = (try? queries.sessionsPerDay(days: days, now: now)) ?? []
        snapshot.perDay = zeroFilled(sparse, days: days, now: now)
        snapshot.providers = ((try? queries.providerBreakdown()) ?? []).map {
            AnalyticsProviderStat(provider: $0.provider, sessions: $0.sessions, userTurns: $0.userTurns)
        }
        snapshot.topProjects = ((try? queries.projectTotals(limit: 8)) ?? []).map {
            AnalyticsProjectStat(
                projectID: $0.projectID, name: $0.projectName, sessions: $0.sessions,
                lastActivity: $0.lastActivity, userTurns: $0.userTurns
            )
        }
        snapshot.streak = (try? queries.currentStreak()) ?? 0
        snapshot.hourHistogram = (try? queries.hourHistogram()) ?? [Int](repeating: 0, count: 24)
        return snapshot
    }

    /// Expands the sparse per-day rows to one entry per window day so the
    /// chart shows quiet days as gaps at the baseline, not missing bars.
    private static func zeroFilled(_ sparse: [(day: String, count: Int)], days: Int, now: Date) -> [AnalyticsDayCount] {
        let counts = Dictionary(sparse.map { ($0.day, $0.count) }, uniquingKeysWith: { first, _ in first })

        // Window day keys are UTC to match SQLite's date() bucketing; the
        // materialized Date is local midnight so axis labels read naturally.
        var utcCalendar = Calendar(identifier: .gregorian)
        utcCalendar.timeZone = TimeZone(identifier: "UTC") ?? .current
        let keyFormatter = DateFormatter()
        keyFormatter.locale = Locale(identifier: "en_US_POSIX")
        keyFormatter.calendar = utcCalendar
        keyFormatter.timeZone = utcCalendar.timeZone
        keyFormatter.dateFormat = "yyyy-MM-dd"
        let localFormatter = DateFormatter()
        localFormatter.locale = Locale(identifier: "en_US_POSIX")
        localFormatter.dateFormat = "yyyy-MM-dd"

        var series: [AnalyticsDayCount] = []
        series.reserveCapacity(days)
        for offset in stride(from: days - 1, through: 0, by: -1) {
            let key = keyFormatter.string(from: now.addingTimeInterval(-Double(offset) * 86_400))
            guard let date = localFormatter.date(from: key) else { continue }
            series.append(AnalyticsDayCount(date: date, count: counts[key] ?? 0))
        }
        return series
    }
}

/// Observable owner of the analytics dashboard state, following the app's
/// model-per-concern pattern. Integration: AppModel holds
/// `let analytics = AnalyticsModel()` and the panel calls `refresh()` when it
/// opens (AnalyticsView already does this in `.task`).
@MainActor
final class AnalyticsModel: ObservableObject {
    @Published private(set) var snapshot: AnalyticsSnapshot = .empty
    @Published private(set) var loading = false

    /// True until the local index has at least one live session row.
    var isEmpty: Bool { snapshot.sessions == 0 }

    /// Reloads every query off the main actor and publishes one snapshot.
    /// Cheap to call on each panel open; concurrent calls coalesce.
    func refresh(days: Int = 30) {
        guard !loading else { return }
        loading = true
        Task.detached(priority: .userInitiated) { [weak self] in
            let snapshot = AnalyticsSnapshot.load(days: days)
            await MainActor.run {
                guard let self else { return }
                self.snapshot = snapshot
                self.loading = false
            }
        }
    }
}
