import Foundation
import SwiftUI
import SpecStoryKit

// MARK: - Tabs

/// The four analytics surfaces, mirroring the cloud web app.
enum AnalyticsTab: String, CaseIterable, Identifiable {
    case overview
    case activity
    case messages
    case ledger

    var id: String { rawValue }

    var title: String {
        switch self {
        case .overview: return "Overview"
        case .activity: return "Activity"
        case .messages: return "Messages"
        case .ledger: return "Ledger"
        }
    }
}

// MARK: - Local snapshot (fallback + first-launch content)

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

/// Immutable result of one local analytics load: every dashboard query, taken
/// together off the main actor. This is the free, offline fallback under the
/// cloud suite: it powers the panel when cloud data has not arrived.
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

// MARK: - Model

/// Observable owner of the analytics dashboard: cloud-first (the full
/// Overview/Activity/Messages/Ledger suite from SpecStory Cloud) with the
/// local SQLite snapshot as fallback content and as the sample underlay for
/// the Pro gate's blurred preview.
///
/// Integration: AppModel holds `let analytics = AnalyticsModel()` (already
/// wired); AnalyticsView calls `configure(auth:)` + `refresh()` in `.task`,
/// mirroring how ProModel is configured from bootstrap.
@MainActor
final class AnalyticsModel: ObservableObject {
    // MARK: Published state

    @Published var tab: AnalyticsTab = .overview
    @Published private(set) var range: AnalyticsRange = .month

    /// Cloud payloads for the selected range; nil until a fetch lands.
    @Published private(set) var overview: AnalyticsOverview?
    @Published private(set) var ledger: AnalyticsLedger?
    @Published private(set) var specScore: AnalyticsSpecScore?
    @Published private(set) var cloudLoading = false
    /// User-facing fetch failure. Nil for the expected degrade states
    /// (signed out, gated, feature dark); those show gate or fallback UI.
    @Published private(set) var cloudError: String?

    /// Local index fallback (always available, even signed out).
    @Published private(set) var snapshot: AnalyticsSnapshot = .empty
    @Published private(set) var localLoading = false

    private var api: CloudAnalyticsAPI?

    /// One bundle per range, kept warm for 5 minutes (the web app's
    /// react-query staleTime) so tab flips and range flips are instant.
    private struct CloudBundle {
        var overview: AnalyticsOverview
        var ledger: AnalyticsLedger?
        var specScore: AnalyticsSpecScore?
        var fetchedAt: Date
    }

    private var cache: [AnalyticsRange: CloudBundle] = [:]
    private var generation = 0
    private static let staleAfter: TimeInterval = 300

    // MARK: Derived

    /// True when the cloud suite has data to draw.
    var hasCloudData: Bool { overview != nil }

    /// True until the local index has at least one live session row.
    var isEmpty: Bool { snapshot.sessions == 0 }

    // MARK: Configuration

    /// Wire the API client to the app's AuthManager. Called from the analytics
    /// panel's `.task`; safe to call repeatedly (first wire wins).
    func configure(auth: AuthManager) {
        guard api == nil else { return }
        api = CloudAnalyticsAPI(
            baseURL: AppModel.cloudBaseURL,
            accessTokenProvider: { [auth] in try await auth.validAccessToken() }
        )
    }

    // MARK: Refresh

    /// Fetches overview + ledger + spec-score for the range (concurrently,
    /// one bundle per range like the web app's shared queries) and reloads
    /// the local snapshot. Cached bundles under 5 minutes old are reused
    /// unless `force`.
    func refresh(range newRange: AnalyticsRange? = nil, force: Bool = false) {
        if let newRange { range = newRange }
        let target = range
        refreshLocal(days: target.dayCount)

        if let cached = cache[target], !force, Date().timeIntervalSince(cached.fetchedAt) < Self.staleAfter {
            overview = cached.overview
            ledger = cached.ledger
            specScore = cached.specScore
            cloudError = nil
            return
        }
        guard let api else { return }

        generation += 1
        let fetchGeneration = generation
        cloudLoading = true
        cloudError = nil
        let timezone = TimeZone.current.identifier

        Task { [weak self] in
            do {
                // One call powers Overview + Activity + Messages headline data;
                // ledger and spec-score power their tabs plus the hero cards.
                async let overviewFetch = api.overview(range: target, timezone: timezone)
                async let ledgerFetch = api.ledger(range: target)
                async let specScoreFetch = api.specScore(range: target)

                let overview = try await overviewFetch
                // Ledger and spec-score are enhancements: their failure must
                // not blank the whole dashboard.
                let ledger = try? await ledgerFetch
                let specScore = try? await specScoreFetch

                guard let self, self.generation == fetchGeneration else { return }
                self.overview = overview
                self.ledger = ledger
                self.specScore = specScore
                self.cache[target] = CloudBundle(overview: overview, ledger: ledger, specScore: specScore, fetchedAt: Date())
                self.cloudLoading = false
            } catch {
                guard let self, self.generation == fetchGeneration else { return }
                self.cloudLoading = false
                switch error {
                case CloudAnalyticsError.unauthorized, CloudAnalyticsError.upgradeRequired, CloudAnalyticsError.featureDark:
                    // Expected degrade states: the gate or the local fallback
                    // covers them; no error banner.
                    self.cloudError = nil
                default:
                    self.cloudError = (error as? CloudAnalyticsError)?.errorDescription ?? error.localizedDescription
                }
            }
        }
    }

    /// Reloads the local SQLite snapshot off the main actor. Cheap to call on
    /// each panel open; concurrent calls coalesce.
    func refreshLocal(days: Int = 30) {
        guard !localLoading else { return }
        localLoading = true
        Task.detached(priority: .userInitiated) { [weak self] in
            let snapshot = AnalyticsSnapshot.load(days: days)
            await MainActor.run {
                guard let self else { return }
                self.snapshot = snapshot
                self.localLoading = false
            }
        }
    }

    // MARK: - Sample fixtures (gated preview underlay)

    /// Deterministic, realistic-looking data for the blurred page behind the
    /// Pro gate, echoing the cloud screenshots: 69% spec-driven starts, a 12
    /// day streak, $1,429.43 estimated spend.
    static let sampleOverview: AnalyticsOverview = makeSampleOverview()
    static let sampleLedger: AnalyticsLedger = makeSampleLedger()
    static let sampleSpecScore: AnalyticsSpecScore = makeSampleSpecScore()

    /// A smooth pseudo-random daily pulse: weekday-heavy, deterministic.
    private static func samplePulse(dayIndex: Int) -> Int {
        let weekday = dayIndex % 7
        let weekend = weekday == 0 || weekday == 6
        let wave = sin(Double(dayIndex) * 0.9) + sin(Double(dayIndex) * 0.37 + 1.3)
        let base = weekend ? 1.6 : 5.2
        return max(0, Int((base + wave * 2.4).rounded()))
    }

    private static func sampleDayKeys(_ days: Int) -> [String] {
        let now = Date()
        return stride(from: days - 1, through: 0, by: -1).map { offset in
            AnalyticsDay.key(for: now.addingTimeInterval(-Double(offset) * 86_400))
        }
    }

    private static func makeSampleOverview() -> AnalyticsOverview {
        let agents = ["claude-code", "codex", "cursor", "gemini"]
        let weights = [0.52, 0.24, 0.16, 0.08]

        var days: [AnalyticsDayAgentCounts] = []
        var totalSessions = 0
        for (index, key) in sampleDayKeys(30).enumerated() {
            let total = samplePulse(dayIndex: index)
            var perAgent: [String: Int] = [:]
            var assigned = 0
            for (agent, weight) in zip(agents, weights) {
                let share = Int((Double(total) * weight).rounded(.down))
                if share > 0 { perAgent[agent] = share }
                assigned += share
            }
            if total > assigned { perAgent["claude-code", default: 0] += total - assigned }
            totalSessions += total
            days.append(AnalyticsDayAgentCounts(date: key, total: total, perAgent: perAgent))
        }

        var cells = [[Int]](repeating: [Int](repeating: 0, count: 24), count: 7)
        for day in 0..<7 {
            for hour in 0..<24 {
                let workday = day >= 1 && day <= 5
                let core = hour >= 9 && hour <= 18
                let evening = hour >= 20 && hour <= 22
                var value = 0
                if workday && core { value = 2 + (day + hour) % 6 }
                if workday && evening { value = 1 + (day + hour) % 3 }
                if !workday && core { value = (day + hour) % 2 }
                cells[day][hour] = value
            }
        }
        cells[2][14] = 9

        let calendar: [AnalyticsCalendarDay] = sampleDayKeys(365).enumerated().map { index, key in
            AnalyticsCalendarDay(date: key, count: max(0, samplePulse(dayIndex: index) - 2))
        }

        let activeDays = days.filter { $0.total > 0 }.count
        return AnalyticsOverview(
            meta: AnalyticsOverviewMeta(
                range: "30d",
                coverage: AnalyticsCoverage(totalSessions: totalSessions, sessionsWithExchanges: totalSessions - 9,
                                            zeroExchangeSessions: 9, durationEligibleSessions: totalSessions - 22,
                                            durationEligibleCoverage: 0.85, distinctAgentCount: 4, distinctProjectCount: 7)
            ),
            agents: [
                AnalyticsAgentUsage(agentName: "claude-code", sessionCount: Int(Double(totalSessions) * 0.52), exchangeCount: 2210),
                AnalyticsAgentUsage(agentName: "codex", sessionCount: Int(Double(totalSessions) * 0.24), exchangeCount: 860),
                AnalyticsAgentUsage(agentName: "cursor", sessionCount: Int(Double(totalSessions) * 0.16), exchangeCount: 490),
                AnalyticsAgentUsage(agentName: "gemini", sessionCount: Int(Double(totalSessions) * 0.08), exchangeCount: 287),
            ],
            agentTotals: AnalyticsAgentTotals(totalSessions: totalSessions, totalExchanges: 3847),
            sessionsByDayByAgent: days,
            punchcard: AnalyticsPunchcard(
                timezone: TimeZone.current.identifier,
                cells: cells,
                peakWindow: AnalyticsPeakWindow(dayOfWeek: 2, hourOfDay: 14, count: 9, label: "Tue 14:00")
            ),
            messages: AnalyticsMessagesSummary(
                totalExchanges: 3847,
                avgExchangesPerSession: 18.4,
                depthDistribution: [
                    AnalyticsDepthBucket(bucket: "1", sessionCount: 20),
                    AnalyticsDepthBucket(bucket: "2-5", sessionCount: 48),
                    AnalyticsDepthBucket(bucket: "6-10", sessionCount: 52),
                    AnalyticsDepthBucket(bucket: "11-25", sessionCount: 56),
                    AnalyticsDepthBucket(bucket: "26-50", sessionCount: 24),
                    AnalyticsDepthBucket(bucket: "50+", sessionCount: 12),
                ],
                turnsByAgent: [
                    AnalyticsAgentTurns(agentName: "claude-code", exchangeCount: 2210),
                    AnalyticsAgentTurns(agentName: "codex", exchangeCount: 860),
                    AnalyticsAgentTurns(agentName: "cursor", exchangeCount: 490),
                    AnalyticsAgentTurns(agentName: "gemini", exchangeCount: 287),
                ],
                longestSession: AnalyticsLongestSession(sessionId: "sample", title: "Ship the watch fleet",
                                                        agentName: "claude-code", exchangeCount: 142)
            ),
            activity: AnalyticsActivitySummary(activeDays: activeDays, currentStreak: 12, longestStreak: 21),
            concurrencyPeak: AnalyticsConcurrencyPeak(eligibleSessions: totalSessions - 22, peak: 4),
            specDrivenStart: AnalyticsSpecDrivenStart(rate: 0.69, sessionsWithFirstPrompt: 180,
                                                      passingSessions: 124, previousRate: 0.61, trendDelta: 0.08),
            topProjects: [
                AnalyticsProjectUsage(projectId: "sample-1", projectName: "getspecstory", sessionCount: 64, exchangeCount: 1204),
                AnalyticsProjectUsage(projectId: "sample-2", projectName: "rookery", sessionCount: 38, exchangeCount: 742),
                AnalyticsProjectUsage(projectId: "sample-3", projectName: "sync-cloud", sessionCount: 22, exchangeCount: 401),
            ],
            highlights: [
                AnalyticsHighlight(id: "sessions", label: "Sessions", current: Double(totalSessions),
                                   previous: 140, deltaAbs: Double(totalSessions - 140), deltaPct: 0.51, direction: "up"),
                AnalyticsHighlight(id: "specRate", label: "Spec-driven", current: 69, previous: 61,
                                   deltaAbs: 8, deltaPct: 0.13, direction: "up"),
                AnalyticsHighlight(id: "messages", label: "Messages", current: 3847, previous: 4210,
                                   deltaAbs: -363, deltaPct: -0.09, direction: "down"),
                AnalyticsHighlight(id: "activeDays", label: "Active days", current: Double(activeDays),
                                   previous: Double(activeDays), deltaAbs: 0, deltaPct: nil, direction: "flat"),
            ],
            activityCalendar: calendar
        )
    }

    private static func makeSampleLedger() -> AnalyticsLedger {
        var spendDays: [AnalyticsSpendDay] = []
        var total = 0.0
        let keys = sampleDayKeys(30)
        for (index, key) in keys.enumerated() {
            let pulse = Double(samplePulse(dayIndex: index))
            guard pulse > 0 else { continue }
            let cost = pulse * 9.4 + sin(Double(index) * 0.7) * 6
            let clamped = max(1.5, cost)
            total += clamped
            spendDays.append(AnalyticsSpendDay(date: key, estCostUsd: clamped,
                                               inputTokens: Int(pulse * 9_000), outputTokens: Int(pulse * 5_400),
                                               cacheCreationTokens: Int(pulse * 21_000), cacheReadTokens: Int(pulse * 96_000)))
        }
        // Nudge the total to the screenshot's number.
        if let last = spendDays.last {
            let adjusted = max(0.5, last.estCostUsd + (1429.43 - total))
            spendDays[spendDays.count - 1] = AnalyticsSpendDay(
                date: last.date, estCostUsd: adjusted, inputTokens: last.inputTokens,
                outputTokens: last.outputTokens, cacheCreationTokens: last.cacheCreationTokens,
                cacheReadTokens: last.cacheReadTokens
            )
        }

        return AnalyticsLedger(
            meta: AnalyticsLedgerMeta(range: "30d"),
            totals: AnalyticsLedgerTotals(estCostUsd: 1429.43, cacheSavingsUsd: 301.20,
                                          inputTokens: 1_250_000, outputTokens: 890_000,
                                          cacheCreationTokens: 3_000_000, cacheReadTokens: 12_500_000,
                                          totalTokens: 17_640_000),
            spendByDay: spendDays,
            spendByAgent: [
                AnalyticsAgentSpend(agentName: "claude-code", estCostUsd: 1105.10, cacheSavingsUsd: 260.40, sessionCount: 120),
                AnalyticsAgentSpend(agentName: "codex", estCostUsd: 214.83, cacheSavingsUsd: 30.10, sessionCount: 48),
                AnalyticsAgentSpend(agentName: "gemini", estCostUsd: 109.50, cacheSavingsUsd: 10.70, sessionCount: 17),
            ],
            spendByProject: [
                AnalyticsProjectSpend(projectId: "sample-1", projectName: "getspecstory", estCostUsd: 740.20, sessionCount: 64),
                AnalyticsProjectSpend(projectId: "sample-2", projectName: "rookery", estCostUsd: 421.90, sessionCount: 38),
            ],
            coverage: AnalyticsLedgerCoverage(totalSessions: 212, coveredSessions: 185, pricedSessions: 185,
                                              excludedByProvider: [
                                                  AnalyticsExcludedProvider(provider: "cursor", sessionCount: 21),
                                                  AnalyticsExcludedProvider(provider: "copilot", sessionCount: 6),
                                              ])
        )
    }

    private static func makeSampleSpecScore() -> AnalyticsSpecScore {
        let trend: [AnalyticsStartRateDay] = sampleDayKeys(30).enumerated().compactMap { index, key in
            let pulse = samplePulse(dayIndex: index)
            guard pulse > 0 else { return nil }
            let rate = min(0.95, max(0.3, 0.62 + sin(Double(index) * 0.5) * 0.18))
            let denom = max(1, pulse - 1)
            return AnalyticsStartRateDay(date: key, rate: rate,
                                         passingSessions: Int((Double(denom) * rate).rounded()),
                                         sessionsWithFirstPrompt: denom)
        }
        return AnalyticsSpecScore(
            meta: AnalyticsSpecScoreMeta(range: "30d", scorerVersion: "specscore-4", llmScorerVersion: "specscore-llm-3"),
            startRate: AnalyticsStartRate(rate: 0.69, passingSessions: 124, sessionsWithFirstPrompt: 180, totalScored: 198),
            startRateTrend: trend,
            gradeDistribution: [
                AnalyticsGradeCount(grade: "A", sessionCount: 38),
                AnalyticsGradeCount(grade: "B", sessionCount: 86),
                AnalyticsGradeCount(grade: "C", sessionCount: 42),
                AnalyticsGradeCount(grade: "D", sessionCount: 20),
                AnalyticsGradeCount(grade: "F", sessionCount: 12),
            ],
            perDimension: [
                AnalyticsDimensionRate(dimension: "constraints", passCount: 96, passRate: 0.53),
                AnalyticsDimensionRate(dimension: "success_criteria", passCount: 72, passRate: 0.40),
                AnalyticsDimensionRate(dimension: "verification", passCount: 84, passRate: 0.47),
                AnalyticsDimensionRate(dimension: "context", passCount: 150, passRate: 0.83),
                AnalyticsDimensionRate(dimension: "specificity", passCount: 132, passRate: 0.73),
            ]
        )
    }
}
