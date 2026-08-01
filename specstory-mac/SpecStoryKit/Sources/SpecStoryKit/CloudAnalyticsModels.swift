import Foundation

// MARK: - Range

/// The shared analytics window: 7, 30, or 90 trailing UTC days. Raw values are
/// the wire strings (`?range=`).
public enum AnalyticsRange: String, CaseIterable, Identifiable, Equatable, Sendable {
    case week = "7d"
    case month = "30d"
    case quarter = "90d"

    public var id: String { rawValue }

    public var dayCount: Int {
        switch self {
        case .week: return 7
        case .month: return 30
        case .quarter: return 90
        }
    }

    public var displayName: String {
        switch self {
        case .week: return "7 days"
        case .month: return "30 days"
        case .quarter: return "90 days"
        }
    }
}

// MARK: - Day keys

/// Analytics day buckets are `YYYY-MM-DD` strings keyed to UTC days.
public enum AnalyticsDay {
    private static let formatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    /// UTC midnight for a `YYYY-MM-DD` key.
    public static func parse(_ key: String?) -> Date? {
        guard let key, !key.isEmpty else { return nil }
        return formatter.date(from: key)
    }

    public static func key(for date: Date) -> String {
        formatter.string(from: date)
    }
}

// MARK: - Tolerant decoding

/// The worker Number()-coerces pg strings before responding, but older rollup
/// cells and admin proxies have leaked numeric strings; decode both.
extension KeyedDecodingContainer {
    func flexInt(_ key: Key) -> Int? {
        if let value = try? decodeIfPresent(Int.self, forKey: key) { return value }
        if let value = try? decodeIfPresent(Double.self, forKey: key) { return Int(value) }
        if let value = try? decodeIfPresent(String.self, forKey: key) {
            if let int = Int(value) { return int }
            if let double = Double(value) { return Int(double) }
        }
        return nil
    }

    func flexDouble(_ key: Key) -> Double? {
        if let value = try? decodeIfPresent(Double.self, forKey: key) { return value }
        if let value = try? decodeIfPresent(Int.self, forKey: key) { return Double(value) }
        if let value = try? decodeIfPresent(String.self, forKey: key) { return Double(value) }
        return nil
    }
}

// MARK: - Overview

/// `GET /api/v1/analytics/overview` payload (`data.overview`).
public struct AnalyticsOverview: Equatable, Sendable {
    public let meta: AnalyticsOverviewMeta
    public let agents: [AnalyticsAgentUsage]
    public let agentTotals: AnalyticsAgentTotals
    /// Zero-filled: one row per UTC day in the window.
    public let sessionsByDayByAgent: [AnalyticsDayAgentCounts]
    public let punchcard: AnalyticsPunchcard
    public let durationsByAgent: [AnalyticsAgentDuration]
    public let messages: AnalyticsMessagesSummary
    /// Window-scoped active days and streaks.
    public let activity: AnalyticsActivitySummary
    public let concurrencyPeak: AnalyticsConcurrencyPeak
    public let specDrivenStart: AnalyticsSpecDrivenStart
    public let topProjects: [AnalyticsProjectUsage]
    /// Top 4 by |deltaPct| already selected server-side.
    public let highlights: [AnalyticsHighlight]
    /// 365 zero-filled trailing-year entries, independent of the range.
    public let activityCalendar: [AnalyticsCalendarDay]

    public init(meta: AnalyticsOverviewMeta = AnalyticsOverviewMeta(),
                agents: [AnalyticsAgentUsage] = [],
                agentTotals: AnalyticsAgentTotals = AnalyticsAgentTotals(),
                sessionsByDayByAgent: [AnalyticsDayAgentCounts] = [],
                punchcard: AnalyticsPunchcard = AnalyticsPunchcard(),
                durationsByAgent: [AnalyticsAgentDuration] = [],
                messages: AnalyticsMessagesSummary = AnalyticsMessagesSummary(),
                activity: AnalyticsActivitySummary = AnalyticsActivitySummary(),
                concurrencyPeak: AnalyticsConcurrencyPeak = AnalyticsConcurrencyPeak(),
                specDrivenStart: AnalyticsSpecDrivenStart = AnalyticsSpecDrivenStart(),
                topProjects: [AnalyticsProjectUsage] = [],
                highlights: [AnalyticsHighlight] = [],
                activityCalendar: [AnalyticsCalendarDay] = []) {
        self.meta = meta
        self.agents = agents
        self.agentTotals = agentTotals
        self.sessionsByDayByAgent = sessionsByDayByAgent
        self.punchcard = punchcard
        self.durationsByAgent = durationsByAgent
        self.messages = messages
        self.activity = activity
        self.concurrencyPeak = concurrencyPeak
        self.specDrivenStart = specDrivenStart
        self.topProjects = topProjects
        self.highlights = highlights
        self.activityCalendar = activityCalendar
    }

    public var isEmpty: Bool { meta.coverage.totalSessions == 0 }
}

extension AnalyticsOverview: Decodable {
    private enum CodingKeys: String, CodingKey {
        case meta, agents, agentTotals, sessionsByDayByAgent, punchcard
        case durationsByAgent, messages, activity, concurrencyPeak
        case specDrivenStart, topProjects, highlights, activityCalendar
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        meta = (try? c.decodeIfPresent(AnalyticsOverviewMeta.self, forKey: .meta)) ?? AnalyticsOverviewMeta()
        agents = (try? c.decodeIfPresent([AnalyticsAgentUsage].self, forKey: .agents)) ?? []
        agentTotals = (try? c.decodeIfPresent(AnalyticsAgentTotals.self, forKey: .agentTotals)) ?? AnalyticsAgentTotals()
        sessionsByDayByAgent = (try? c.decodeIfPresent([AnalyticsDayAgentCounts].self, forKey: .sessionsByDayByAgent)) ?? []
        punchcard = (try? c.decodeIfPresent(AnalyticsPunchcard.self, forKey: .punchcard)) ?? AnalyticsPunchcard()
        durationsByAgent = (try? c.decodeIfPresent([AnalyticsAgentDuration].self, forKey: .durationsByAgent)) ?? []
        messages = (try? c.decodeIfPresent(AnalyticsMessagesSummary.self, forKey: .messages)) ?? AnalyticsMessagesSummary()
        activity = (try? c.decodeIfPresent(AnalyticsActivitySummary.self, forKey: .activity)) ?? AnalyticsActivitySummary()
        concurrencyPeak = (try? c.decodeIfPresent(AnalyticsConcurrencyPeak.self, forKey: .concurrencyPeak)) ?? AnalyticsConcurrencyPeak()
        specDrivenStart = (try? c.decodeIfPresent(AnalyticsSpecDrivenStart.self, forKey: .specDrivenStart)) ?? AnalyticsSpecDrivenStart()
        topProjects = (try? c.decodeIfPresent([AnalyticsProjectUsage].self, forKey: .topProjects)) ?? []
        highlights = (try? c.decodeIfPresent([AnalyticsHighlight].self, forKey: .highlights)) ?? []
        activityCalendar = (try? c.decodeIfPresent([AnalyticsCalendarDay].self, forKey: .activityCalendar)) ?? []
    }
}

public struct AnalyticsOverviewMeta: Equatable, Sendable {
    public let range: String
    public let fromIso: String
    public let toIso: String
    public let previousFromIso: String?
    public let previousToIso: String?
    public let timezone: String
    public let generatedAtIso: String?
    public let coverage: AnalyticsCoverage

    public var fromDate: Date? { CloudDate.parse(fromIso) }
    public var toDate: Date? { CloudDate.parse(toIso) }

    public init(range: String = "30d", fromIso: String = "", toIso: String = "",
                previousFromIso: String? = nil, previousToIso: String? = nil,
                timezone: String = "UTC", generatedAtIso: String? = nil,
                coverage: AnalyticsCoverage = AnalyticsCoverage()) {
        self.range = range
        self.fromIso = fromIso
        self.toIso = toIso
        self.previousFromIso = previousFromIso
        self.previousToIso = previousToIso
        self.timezone = timezone
        self.generatedAtIso = generatedAtIso
        self.coverage = coverage
    }
}

extension AnalyticsOverviewMeta: Decodable {
    private enum CodingKeys: String, CodingKey {
        case range, fromIso, toIso, previousFromIso, previousToIso
        case timezone, generatedAtIso, coverage
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        range = (try? c.decodeIfPresent(String.self, forKey: .range)) ?? "30d"
        fromIso = (try? c.decodeIfPresent(String.self, forKey: .fromIso)) ?? ""
        toIso = (try? c.decodeIfPresent(String.self, forKey: .toIso)) ?? ""
        previousFromIso = try? c.decodeIfPresent(String.self, forKey: .previousFromIso)
        previousToIso = try? c.decodeIfPresent(String.self, forKey: .previousToIso)
        timezone = (try? c.decodeIfPresent(String.self, forKey: .timezone)) ?? "UTC"
        generatedAtIso = try? c.decodeIfPresent(String.self, forKey: .generatedAtIso)
        coverage = (try? c.decodeIfPresent(AnalyticsCoverage.self, forKey: .coverage)) ?? AnalyticsCoverage()
    }
}

public struct AnalyticsCoverage: Equatable, Sendable {
    public let totalSessions: Int
    public let sessionsWithExchanges: Int
    public let zeroExchangeSessions: Int
    public let nullTimestampSessions: Int
    public let cursorZeroWidthSessions: Int
    public let durationEligibleSessions: Int
    /// Ratio 0..1.
    public let durationEligibleCoverage: Double
    public let distinctAgentCount: Int
    public let distinctProjectCount: Int

    public init(totalSessions: Int = 0, sessionsWithExchanges: Int = 0,
                zeroExchangeSessions: Int = 0, nullTimestampSessions: Int = 0,
                cursorZeroWidthSessions: Int = 0, durationEligibleSessions: Int = 0,
                durationEligibleCoverage: Double = 0, distinctAgentCount: Int = 0,
                distinctProjectCount: Int = 0) {
        self.totalSessions = totalSessions
        self.sessionsWithExchanges = sessionsWithExchanges
        self.zeroExchangeSessions = zeroExchangeSessions
        self.nullTimestampSessions = nullTimestampSessions
        self.cursorZeroWidthSessions = cursorZeroWidthSessions
        self.durationEligibleSessions = durationEligibleSessions
        self.durationEligibleCoverage = durationEligibleCoverage
        self.distinctAgentCount = distinctAgentCount
        self.distinctProjectCount = distinctProjectCount
    }
}

extension AnalyticsCoverage: Decodable {
    private enum CodingKeys: String, CodingKey {
        case totalSessions, sessionsWithExchanges, zeroExchangeSessions
        case nullTimestampSessions, cursorZeroWidthSessions
        case durationEligibleSessions, durationEligibleCoverage
        case distinctAgentCount, distinctProjectCount
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        totalSessions = c.flexInt(.totalSessions) ?? 0
        sessionsWithExchanges = c.flexInt(.sessionsWithExchanges) ?? 0
        zeroExchangeSessions = c.flexInt(.zeroExchangeSessions) ?? 0
        nullTimestampSessions = c.flexInt(.nullTimestampSessions) ?? 0
        cursorZeroWidthSessions = c.flexInt(.cursorZeroWidthSessions) ?? 0
        durationEligibleSessions = c.flexInt(.durationEligibleSessions) ?? 0
        durationEligibleCoverage = c.flexDouble(.durationEligibleCoverage) ?? 0
        distinctAgentCount = c.flexInt(.distinctAgentCount) ?? 0
        distinctProjectCount = c.flexInt(.distinctProjectCount) ?? 0
    }
}

public struct AnalyticsAgentUsage: Equatable, Sendable, Identifiable {
    public let agentName: String
    public let sessionCount: Int
    public let exchangeCount: Int
    public let lastUsedIso: String?

    public var id: String { agentName }
    public var lastUsedDate: Date? { CloudDate.parse(lastUsedIso) }

    public init(agentName: String, sessionCount: Int = 0, exchangeCount: Int = 0, lastUsedIso: String? = nil) {
        self.agentName = agentName
        self.sessionCount = sessionCount
        self.exchangeCount = exchangeCount
        self.lastUsedIso = lastUsedIso
    }
}

extension AnalyticsAgentUsage: Decodable {
    private enum CodingKeys: String, CodingKey { case agentName, sessionCount, exchangeCount, lastUsedIso }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        sessionCount = c.flexInt(.sessionCount) ?? 0
        exchangeCount = c.flexInt(.exchangeCount) ?? 0
        lastUsedIso = try? c.decodeIfPresent(String.self, forKey: .lastUsedIso)
    }
}

public struct AnalyticsAgentShare: Equatable, Sendable, Identifiable {
    public let agentName: String
    public let sessionCount: Int
    public let pct: Double

    public var id: String { agentName }

    public init(agentName: String, sessionCount: Int = 0, pct: Double = 0) {
        self.agentName = agentName
        self.sessionCount = sessionCount
        self.pct = pct
    }
}

extension AnalyticsAgentShare: Decodable {
    private enum CodingKeys: String, CodingKey { case agentName, sessionCount, pct }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        sessionCount = c.flexInt(.sessionCount) ?? 0
        pct = c.flexDouble(.pct) ?? 0
    }
}

public struct AnalyticsAgentTotals: Equatable, Sendable {
    public let totalSessions: Int
    public let totalExchanges: Int
    public let byAgent: [AnalyticsAgentShare]

    public init(totalSessions: Int = 0, totalExchanges: Int = 0, byAgent: [AnalyticsAgentShare] = []) {
        self.totalSessions = totalSessions
        self.totalExchanges = totalExchanges
        self.byAgent = byAgent
    }
}

extension AnalyticsAgentTotals: Decodable {
    private enum CodingKeys: String, CodingKey { case totalSessions, totalExchanges, byAgent }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        totalSessions = c.flexInt(.totalSessions) ?? 0
        totalExchanges = c.flexInt(.totalExchanges) ?? 0
        byAgent = (try? c.decodeIfPresent([AnalyticsAgentShare].self, forKey: .byAgent)) ?? []
    }
}

/// One zero-filled day of the stacked-by-agent series.
public struct AnalyticsDayAgentCounts: Equatable, Sendable, Identifiable {
    /// `YYYY-MM-DD` UTC day key.
    public let date: String
    public let total: Int
    public let perAgent: [String: Int]

    public var id: String { date }
    public var dayDate: Date? { AnalyticsDay.parse(date) }

    public init(date: String, total: Int = 0, perAgent: [String: Int] = [:]) {
        self.date = date
        self.total = total
        self.perAgent = perAgent
    }
}

extension AnalyticsDayAgentCounts: Decodable {
    private enum CodingKeys: String, CodingKey { case date, total, perAgent }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        date = (try? c.decodeIfPresent(String.self, forKey: .date)) ?? ""
        total = c.flexInt(.total) ?? 0
        perAgent = (try? c.decodeIfPresent([String: Int].self, forKey: .perAgent)) ?? [:]
    }
}

public struct AnalyticsPeakWindow: Equatable, Sendable {
    /// 0 = Sunday .. 6 = Saturday.
    public let dayOfWeek: Int
    public let hourOfDay: Int
    public let count: Int
    /// Pre-rendered "Tue 14:00" (plus " UTC" only when the zone is UTC).
    public let label: String

    public init(dayOfWeek: Int = 0, hourOfDay: Int = 0, count: Int = 0, label: String = "") {
        self.dayOfWeek = dayOfWeek
        self.hourOfDay = hourOfDay
        self.count = count
        self.label = label
    }
}

extension AnalyticsPeakWindow: Decodable {
    private enum CodingKeys: String, CodingKey { case dayOfWeek, hourOfDay, count, label }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        dayOfWeek = c.flexInt(.dayOfWeek) ?? 0
        hourOfDay = c.flexInt(.hourOfDay) ?? 0
        count = c.flexInt(.count) ?? 0
        label = (try? c.decodeIfPresent(String.self, forKey: .label)) ?? ""
    }
}

/// Hour-by-weekday punchcard, bucketed in the requested timezone.
public struct AnalyticsPunchcard: Equatable, Sendable {
    public let timezone: String
    /// `[dayOfWeek 0=Sun..6][hour 0..23]`, zero-filled 7 x 24.
    public let cells: [[Int]]
    public let peakWindow: AnalyticsPeakWindow?

    public init(timezone: String = "UTC", cells: [[Int]] = [], peakWindow: AnalyticsPeakWindow? = nil) {
        self.timezone = timezone
        self.cells = cells
        self.peakWindow = peakWindow
    }

    public func count(dayOfWeek: Int, hour: Int) -> Int {
        guard cells.indices.contains(dayOfWeek), cells[dayOfWeek].indices.contains(hour) else { return 0 }
        return cells[dayOfWeek][hour]
    }

    public var maxCount: Int { cells.flatMap { $0 }.max() ?? 0 }
}

extension AnalyticsPunchcard: Decodable {
    private enum CodingKeys: String, CodingKey { case timezone, cells, peakWindow }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        timezone = (try? c.decodeIfPresent(String.self, forKey: .timezone)) ?? "UTC"
        cells = (try? c.decodeIfPresent([[Int]].self, forKey: .cells)) ?? []
        peakWindow = try? c.decodeIfPresent(AnalyticsPeakWindow.self, forKey: .peakWindow)
    }
}

public struct AnalyticsAgentDuration: Equatable, Sendable, Identifiable {
    public let agentName: String
    public let eligibleSessions: Int
    public let medianMs: Double
    public let p90Ms: Double
    public let totalMs: Double

    public var id: String { agentName }

    public init(agentName: String, eligibleSessions: Int = 0, medianMs: Double = 0,
                p90Ms: Double = 0, totalMs: Double = 0) {
        self.agentName = agentName
        self.eligibleSessions = eligibleSessions
        self.medianMs = medianMs
        self.p90Ms = p90Ms
        self.totalMs = totalMs
    }
}

extension AnalyticsAgentDuration: Decodable {
    private enum CodingKeys: String, CodingKey { case agentName, eligibleSessions, medianMs, p90Ms, totalMs }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        eligibleSessions = c.flexInt(.eligibleSessions) ?? 0
        medianMs = c.flexDouble(.medianMs) ?? 0
        p90Ms = c.flexDouble(.p90Ms) ?? 0
        totalMs = c.flexDouble(.totalMs) ?? 0
    }
}

/// Depth buckets arrive as all six of "1", "2-5", "6-10", "11-25", "26-50", "50+".
public struct AnalyticsDepthBucket: Equatable, Sendable, Identifiable {
    public let bucket: String
    public let sessionCount: Int

    public var id: String { bucket }

    public init(bucket: String, sessionCount: Int = 0) {
        self.bucket = bucket
        self.sessionCount = sessionCount
    }
}

extension AnalyticsDepthBucket: Decodable {
    private enum CodingKeys: String, CodingKey { case bucket, sessionCount }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        bucket = (try? c.decodeIfPresent(String.self, forKey: .bucket)) ?? ""
        sessionCount = c.flexInt(.sessionCount) ?? 0
    }
}

public struct AnalyticsAgentTurns: Equatable, Sendable, Identifiable {
    public let agentName: String
    public let exchangeCount: Int

    public var id: String { agentName }

    public init(agentName: String, exchangeCount: Int = 0) {
        self.agentName = agentName
        self.exchangeCount = exchangeCount
    }
}

extension AnalyticsAgentTurns: Decodable {
    private enum CodingKeys: String, CodingKey { case agentName, exchangeCount }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        exchangeCount = c.flexInt(.exchangeCount) ?? 0
    }
}

public struct AnalyticsLongestSession: Equatable, Sendable {
    public let sessionId: String
    public let title: String?
    public let agentName: String
    public let exchangeCount: Int

    public init(sessionId: String = "", title: String? = nil, agentName: String = "Unknown", exchangeCount: Int = 0) {
        self.sessionId = sessionId
        self.title = title
        self.agentName = agentName
        self.exchangeCount = exchangeCount
    }
}

extension AnalyticsLongestSession: Decodable {
    private enum CodingKeys: String, CodingKey { case sessionId, title, agentName, exchangeCount }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionId = (try? c.decodeIfPresent(String.self, forKey: .sessionId)) ?? ""
        title = try? c.decodeIfPresent(String.self, forKey: .title)
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        exchangeCount = c.flexInt(.exchangeCount) ?? 0
    }
}

public struct AnalyticsMessagesSummary: Equatable, Sendable {
    public let totalExchanges: Int
    /// Rounded to 2dp server-side; denominator is ALL window sessions.
    public let avgExchangesPerSession: Double
    public let depthDistribution: [AnalyticsDepthBucket]
    public let turnsByAgent: [AnalyticsAgentTurns]
    public let longestSession: AnalyticsLongestSession?

    public init(totalExchanges: Int = 0, avgExchangesPerSession: Double = 0,
                depthDistribution: [AnalyticsDepthBucket] = [],
                turnsByAgent: [AnalyticsAgentTurns] = [],
                longestSession: AnalyticsLongestSession? = nil) {
        self.totalExchanges = totalExchanges
        self.avgExchangesPerSession = avgExchangesPerSession
        self.depthDistribution = depthDistribution
        self.turnsByAgent = turnsByAgent
        self.longestSession = longestSession
    }
}

extension AnalyticsMessagesSummary: Decodable {
    private enum CodingKeys: String, CodingKey {
        case totalExchanges, avgExchangesPerSession, depthDistribution, turnsByAgent, longestSession
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        totalExchanges = c.flexInt(.totalExchanges) ?? 0
        avgExchangesPerSession = c.flexDouble(.avgExchangesPerSession) ?? 0
        depthDistribution = (try? c.decodeIfPresent([AnalyticsDepthBucket].self, forKey: .depthDistribution)) ?? []
        turnsByAgent = (try? c.decodeIfPresent([AnalyticsAgentTurns].self, forKey: .turnsByAgent)) ?? []
        longestSession = try? c.decodeIfPresent(AnalyticsLongestSession.self, forKey: .longestSession)
    }
}

public struct AnalyticsActivitySummary: Equatable, Sendable {
    public let activeDays: Int
    public let currentStreak: Int
    public let longestStreak: Int

    public init(activeDays: Int = 0, currentStreak: Int = 0, longestStreak: Int = 0) {
        self.activeDays = activeDays
        self.currentStreak = currentStreak
        self.longestStreak = longestStreak
    }
}

extension AnalyticsActivitySummary: Decodable {
    private enum CodingKeys: String, CodingKey { case activeDays, currentStreak, longestStreak }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        activeDays = c.flexInt(.activeDays) ?? 0
        currentStreak = c.flexInt(.currentStreak) ?? 0
        longestStreak = c.flexInt(.longestStreak) ?? 0
    }
}

public struct AnalyticsConcurrencyPeak: Equatable, Sendable {
    public let eligibleSessions: Int
    public let peak: Int
    public let peakAtIso: String?

    public var peakAtDate: Date? { CloudDate.parse(peakAtIso) }

    public init(eligibleSessions: Int = 0, peak: Int = 0, peakAtIso: String? = nil) {
        self.eligibleSessions = eligibleSessions
        self.peak = peak
        self.peakAtIso = peakAtIso
    }
}

extension AnalyticsConcurrencyPeak: Decodable {
    private enum CodingKeys: String, CodingKey { case eligibleSessions, peak, peakAtIso }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        eligibleSessions = c.flexInt(.eligibleSessions) ?? 0
        peak = c.flexInt(.peak) ?? 0
        peakAtIso = try? c.decodeIfPresent(String.self, forKey: .peakAtIso)
    }
}

/// The overview's embedded v0 heuristic spec-driven rate (fallback while the
/// real spec-score payload loads). Rates are 0..1 fractions.
public struct AnalyticsSpecDrivenStart: Equatable, Sendable {
    public let version: String
    public let rate: Double?
    public let sessionsWithFirstPrompt: Int
    public let passingSessions: Int
    public let previousRate: Double?
    /// current minus previous rate, round2; null when either window is empty.
    public let trendDelta: Double?

    public init(version: String = "v0-heuristic", rate: Double? = nil,
                sessionsWithFirstPrompt: Int = 0, passingSessions: Int = 0,
                previousRate: Double? = nil, trendDelta: Double? = nil) {
        self.version = version
        self.rate = rate
        self.sessionsWithFirstPrompt = sessionsWithFirstPrompt
        self.passingSessions = passingSessions
        self.previousRate = previousRate
        self.trendDelta = trendDelta
    }
}

extension AnalyticsSpecDrivenStart: Decodable {
    private enum CodingKeys: String, CodingKey {
        case version, rate, sessionsWithFirstPrompt, passingSessions, previousRate, trendDelta
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = (try? c.decodeIfPresent(String.self, forKey: .version)) ?? "v0-heuristic"
        rate = c.flexDouble(.rate)
        sessionsWithFirstPrompt = c.flexInt(.sessionsWithFirstPrompt) ?? 0
        passingSessions = c.flexInt(.passingSessions) ?? 0
        previousRate = c.flexDouble(.previousRate)
        trendDelta = c.flexDouble(.trendDelta)
    }
}

public struct AnalyticsProjectUsage: Equatable, Sendable, Identifiable {
    public let projectId: String
    public let projectName: String?
    public let sessionCount: Int
    public let exchangeCount: Int
    public let lastActiveIso: String?

    public var id: String { projectId }
    public var lastActiveDate: Date? { CloudDate.parse(lastActiveIso) }

    public init(projectId: String, projectName: String? = nil, sessionCount: Int = 0,
                exchangeCount: Int = 0, lastActiveIso: String? = nil) {
        self.projectId = projectId
        self.projectName = projectName
        self.sessionCount = sessionCount
        self.exchangeCount = exchangeCount
        self.lastActiveIso = lastActiveIso
    }
}

extension AnalyticsProjectUsage: Decodable {
    private enum CodingKeys: String, CodingKey { case projectId, projectName, sessionCount, exchangeCount, lastActiveIso }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        projectId = (try? c.decodeIfPresent(String.self, forKey: .projectId)) ?? ""
        projectName = try? c.decodeIfPresent(String.self, forKey: .projectName)
        sessionCount = c.flexInt(.sessionCount) ?? 0
        exchangeCount = c.flexInt(.exchangeCount) ?? 0
        lastActiveIso = try? c.decodeIfPresent(String.self, forKey: .lastActiveIso)
    }
}

/// Window-vs-previous highlight. `current`/`previous` are counts except for
/// the `specRate` highlight, which is expressed 0-100.
public struct AnalyticsHighlight: Equatable, Sendable, Identifiable {
    public let id: String
    public let label: String
    public let current: Double
    public let previous: Double
    public let deltaAbs: Double
    /// deltaAbs / previous, null when previous is 0.
    public let deltaPct: Double?
    /// "up" | "down" | "flat".
    public let direction: String

    public var isUp: Bool { direction == "up" }
    public var isDown: Bool { direction == "down" }

    public init(id: String, label: String, current: Double = 0, previous: Double = 0,
                deltaAbs: Double = 0, deltaPct: Double? = nil, direction: String = "flat") {
        self.id = id
        self.label = label
        self.current = current
        self.previous = previous
        self.deltaAbs = deltaAbs
        self.deltaPct = deltaPct
        self.direction = direction
    }
}

extension AnalyticsHighlight: Decodable {
    private enum CodingKeys: String, CodingKey { case id, label, current, previous, deltaAbs, deltaPct, direction }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = (try? c.decodeIfPresent(String.self, forKey: .id)) ?? UUID().uuidString
        label = (try? c.decodeIfPresent(String.self, forKey: .label)) ?? ""
        current = c.flexDouble(.current) ?? 0
        previous = c.flexDouble(.previous) ?? 0
        deltaAbs = c.flexDouble(.deltaAbs) ?? 0
        deltaPct = c.flexDouble(.deltaPct)
        direction = (try? c.decodeIfPresent(String.self, forKey: .direction)) ?? "flat"
    }
}

public struct AnalyticsCalendarDay: Equatable, Sendable, Identifiable {
    /// `YYYY-MM-DD` UTC day key.
    public let date: String
    public let count: Int

    public var id: String { date }
    public var dayDate: Date? { AnalyticsDay.parse(date) }

    public init(date: String, count: Int = 0) {
        self.date = date
        self.count = count
    }
}

extension AnalyticsCalendarDay: Decodable {
    private enum CodingKeys: String, CodingKey { case date, count }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        date = (try? c.decodeIfPresent(String.self, forKey: .date)) ?? ""
        count = c.flexInt(.count) ?? 0
    }
}

// MARK: - Sessions drill-down

/// One row from `GET /api/v1/analytics/sessions` (cap 500, newest first).
public struct AnalyticsSessionRow: Equatable, Sendable, Identifiable {
    public let id: String
    public let clientId: String
    /// userTitle ?? name; may be null server-side.
    public let name: String?
    public let fileName: String?
    /// Resolved agent, never null (NULL/'' arrives as "Unknown").
    public let agentName: String
    public let startedAt: String?
    public let endedAt: String?
    public let updatedAt: String?
    /// Always set: 'YYYY-MM-DDTHH:MM:SSZ'.
    public let bucketIso: String
    public let exchangeCount: Int
    public let projectId: String?
    public let projectName: String?
    public let projectColor: String?
    /// First exchange content sliced to 600 chars.
    public let firstPrompt: String?

    public var bucketDate: Date? { CloudDate.parse(bucketIso) }
    public var startedAtDate: Date? { CloudDate.parse(startedAt) }
    public var endedAtDate: Date? { CloudDate.parse(endedAt) }

    public init(id: String, clientId: String = "", name: String? = nil, fileName: String? = nil,
                agentName: String = "Unknown", startedAt: String? = nil, endedAt: String? = nil,
                updatedAt: String? = nil, bucketIso: String = "", exchangeCount: Int = 0,
                projectId: String? = nil, projectName: String? = nil, projectColor: String? = nil,
                firstPrompt: String? = nil) {
        self.id = id
        self.clientId = clientId
        self.name = name
        self.fileName = fileName
        self.agentName = agentName
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.updatedAt = updatedAt
        self.bucketIso = bucketIso
        self.exchangeCount = exchangeCount
        self.projectId = projectId
        self.projectName = projectName
        self.projectColor = projectColor
        self.firstPrompt = firstPrompt
    }
}

extension AnalyticsSessionRow: Decodable {
    private enum CodingKeys: String, CodingKey {
        case id, clientId, name, fileName, agentName, startedAt, endedAt, updatedAt
        case bucketIso, exchangeCount, projectId, projectName, projectColor, firstPrompt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = (try? c.decodeIfPresent(String.self, forKey: .id)) ?? ""
        clientId = (try? c.decodeIfPresent(String.self, forKey: .clientId)) ?? ""
        name = try? c.decodeIfPresent(String.self, forKey: .name)
        fileName = try? c.decodeIfPresent(String.self, forKey: .fileName)
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        startedAt = try? c.decodeIfPresent(String.self, forKey: .startedAt)
        endedAt = try? c.decodeIfPresent(String.self, forKey: .endedAt)
        updatedAt = try? c.decodeIfPresent(String.self, forKey: .updatedAt)
        bucketIso = (try? c.decodeIfPresent(String.self, forKey: .bucketIso)) ?? ""
        exchangeCount = c.flexInt(.exchangeCount) ?? 0
        projectId = try? c.decodeIfPresent(String.self, forKey: .projectId)
        projectName = try? c.decodeIfPresent(String.self, forKey: .projectName)
        projectColor = try? c.decodeIfPresent(String.self, forKey: .projectColor)
        firstPrompt = try? c.decodeIfPresent(String.self, forKey: .firstPrompt)
    }
}

// MARK: - Ledger

public struct AnalyticsLedgerMeta: Equatable, Sendable {
    public let range: String
    public let fromIso: String
    public let toIso: String
    public let timezone: String
    public let generatedAtIso: String?

    public var fromDate: Date? { CloudDate.parse(fromIso) }
    public var toDate: Date? { CloudDate.parse(toIso) }

    public init(range: String = "30d", fromIso: String = "", toIso: String = "",
                timezone: String = "UTC", generatedAtIso: String? = nil) {
        self.range = range
        self.fromIso = fromIso
        self.toIso = toIso
        self.timezone = timezone
        self.generatedAtIso = generatedAtIso
    }
}

extension AnalyticsLedgerMeta: Decodable {
    private enum CodingKeys: String, CodingKey { case range, fromIso, toIso, timezone, generatedAtIso }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        range = (try? c.decodeIfPresent(String.self, forKey: .range)) ?? "30d"
        fromIso = (try? c.decodeIfPresent(String.self, forKey: .fromIso)) ?? ""
        toIso = (try? c.decodeIfPresent(String.self, forKey: .toIso)) ?? ""
        timezone = (try? c.decodeIfPresent(String.self, forKey: .timezone)) ?? "UTC"
        generatedAtIso = try? c.decodeIfPresent(String.self, forKey: .generatedAtIso)
    }
}

public struct AnalyticsLedgerTotals: Equatable, Sendable {
    public let estCostUsd: Double
    public let cacheSavingsUsd: Double
    public let inputTokens: Int
    public let outputTokens: Int
    public let cacheCreationTokens: Int
    public let cacheReadTokens: Int
    public let totalTokens: Int

    public init(estCostUsd: Double = 0, cacheSavingsUsd: Double = 0, inputTokens: Int = 0,
                outputTokens: Int = 0, cacheCreationTokens: Int = 0, cacheReadTokens: Int = 0,
                totalTokens: Int = 0) {
        self.estCostUsd = estCostUsd
        self.cacheSavingsUsd = cacheSavingsUsd
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.cacheCreationTokens = cacheCreationTokens
        self.cacheReadTokens = cacheReadTokens
        self.totalTokens = totalTokens
    }
}

extension AnalyticsLedgerTotals: Decodable {
    private enum CodingKeys: String, CodingKey {
        case estCostUsd, cacheSavingsUsd, inputTokens, outputTokens
        case cacheCreationTokens, cacheReadTokens, totalTokens
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        estCostUsd = c.flexDouble(.estCostUsd) ?? 0
        cacheSavingsUsd = c.flexDouble(.cacheSavingsUsd) ?? 0
        inputTokens = c.flexInt(.inputTokens) ?? 0
        outputTokens = c.flexInt(.outputTokens) ?? 0
        cacheCreationTokens = c.flexInt(.cacheCreationTokens) ?? 0
        cacheReadTokens = c.flexInt(.cacheReadTokens) ?? 0
        totalTokens = c.flexInt(.totalTokens) ?? 0
    }
}

/// One day of spend. NOT zero-filled: only days with data, ascending.
public struct AnalyticsSpendDay: Equatable, Sendable, Identifiable {
    public let date: String
    public let estCostUsd: Double
    public let inputTokens: Int
    public let outputTokens: Int
    public let cacheCreationTokens: Int
    public let cacheReadTokens: Int

    public var id: String { date }
    public var dayDate: Date? { AnalyticsDay.parse(date) }

    public init(date: String, estCostUsd: Double = 0, inputTokens: Int = 0, outputTokens: Int = 0,
                cacheCreationTokens: Int = 0, cacheReadTokens: Int = 0) {
        self.date = date
        self.estCostUsd = estCostUsd
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.cacheCreationTokens = cacheCreationTokens
        self.cacheReadTokens = cacheReadTokens
    }
}

extension AnalyticsSpendDay: Decodable {
    private enum CodingKeys: String, CodingKey {
        case date, estCostUsd, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        date = (try? c.decodeIfPresent(String.self, forKey: .date)) ?? ""
        estCostUsd = c.flexDouble(.estCostUsd) ?? 0
        inputTokens = c.flexInt(.inputTokens) ?? 0
        outputTokens = c.flexInt(.outputTokens) ?? 0
        cacheCreationTokens = c.flexInt(.cacheCreationTokens) ?? 0
        cacheReadTokens = c.flexInt(.cacheReadTokens) ?? 0
    }
}

public struct AnalyticsAgentSpend: Equatable, Sendable, Identifiable {
    public let agentName: String
    public let estCostUsd: Double
    public let cacheSavingsUsd: Double
    public let sessionCount: Int

    public var id: String { agentName }

    public init(agentName: String, estCostUsd: Double = 0, cacheSavingsUsd: Double = 0, sessionCount: Int = 0) {
        self.agentName = agentName
        self.estCostUsd = estCostUsd
        self.cacheSavingsUsd = cacheSavingsUsd
        self.sessionCount = sessionCount
    }
}

extension AnalyticsAgentSpend: Decodable {
    private enum CodingKeys: String, CodingKey { case agentName, estCostUsd, cacheSavingsUsd, sessionCount }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        estCostUsd = c.flexDouble(.estCostUsd) ?? 0
        cacheSavingsUsd = c.flexDouble(.cacheSavingsUsd) ?? 0
        sessionCount = c.flexInt(.sessionCount) ?? 0
    }
}

public struct AnalyticsProjectSpend: Equatable, Sendable, Identifiable {
    public let projectId: String
    public let projectName: String?
    public let estCostUsd: Double
    public let sessionCount: Int

    public var id: String { projectId }

    public init(projectId: String, projectName: String? = nil, estCostUsd: Double = 0, sessionCount: Int = 0) {
        self.projectId = projectId
        self.projectName = projectName
        self.estCostUsd = estCostUsd
        self.sessionCount = sessionCount
    }
}

extension AnalyticsProjectSpend: Decodable {
    private enum CodingKeys: String, CodingKey { case projectId, projectName, estCostUsd, sessionCount }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        projectId = (try? c.decodeIfPresent(String.self, forKey: .projectId)) ?? ""
        projectName = try? c.decodeIfPresent(String.self, forKey: .projectName)
        estCostUsd = c.flexDouble(.estCostUsd) ?? 0
        sessionCount = c.flexInt(.sessionCount) ?? 0
    }
}

public struct AnalyticsExcludedProvider: Equatable, Sendable, Identifiable {
    public let provider: String
    public let sessionCount: Int

    public var id: String { provider }

    public init(provider: String, sessionCount: Int = 0) {
        self.provider = provider
        self.sessionCount = sessionCount
    }
}

extension AnalyticsExcludedProvider: Decodable {
    private enum CodingKeys: String, CodingKey { case provider, sessionCount }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        provider = (try? c.decodeIfPresent(String.self, forKey: .provider)) ?? ""
        sessionCount = c.flexInt(.sessionCount) ?? 0
    }
}

public struct AnalyticsLedgerCoverage: Equatable, Sendable {
    public let totalSessions: Int
    public let coveredSessions: Int
    public let pricedSessions: Int
    public let excludedByProvider: [AnalyticsExcludedProvider]

    public init(totalSessions: Int = 0, coveredSessions: Int = 0, pricedSessions: Int = 0,
                excludedByProvider: [AnalyticsExcludedProvider] = []) {
        self.totalSessions = totalSessions
        self.coveredSessions = coveredSessions
        self.pricedSessions = pricedSessions
        self.excludedByProvider = excludedByProvider
    }
}

extension AnalyticsLedgerCoverage: Decodable {
    private enum CodingKeys: String, CodingKey { case totalSessions, coveredSessions, pricedSessions, excludedByProvider }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        totalSessions = c.flexInt(.totalSessions) ?? 0
        coveredSessions = c.flexInt(.coveredSessions) ?? 0
        pricedSessions = c.flexInt(.pricedSessions) ?? 0
        excludedByProvider = (try? c.decodeIfPresent([AnalyticsExcludedProvider].self, forKey: .excludedByProvider)) ?? []
    }
}

/// `GET /api/v1/analytics/ledger` payload (`data.ledger`). Estimated
/// public-rate cost, not a bill.
public struct AnalyticsLedger: Equatable, Sendable {
    public let meta: AnalyticsLedgerMeta
    public let totals: AnalyticsLedgerTotals
    public let spendByDay: [AnalyticsSpendDay]
    public let spendByAgent: [AnalyticsAgentSpend]
    public let spendByProject: [AnalyticsProjectSpend]
    public let coverage: AnalyticsLedgerCoverage

    public init(meta: AnalyticsLedgerMeta = AnalyticsLedgerMeta(),
                totals: AnalyticsLedgerTotals = AnalyticsLedgerTotals(),
                spendByDay: [AnalyticsSpendDay] = [],
                spendByAgent: [AnalyticsAgentSpend] = [],
                spendByProject: [AnalyticsProjectSpend] = [],
                coverage: AnalyticsLedgerCoverage = AnalyticsLedgerCoverage()) {
        self.meta = meta
        self.totals = totals
        self.spendByDay = spendByDay
        self.spendByAgent = spendByAgent
        self.spendByProject = spendByProject
        self.coverage = coverage
    }

    public var isEmpty: Bool { coverage.totalSessions == 0 }
    /// Days that carry any spend (the ledger's "active day" denominator).
    public var activeSpendDays: Int { spendByDay.filter { $0.estCostUsd > 0 }.count }
}

extension AnalyticsLedger: Decodable {
    private enum CodingKeys: String, CodingKey { case meta, totals, spendByDay, spendByAgent, spendByProject, coverage }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        meta = (try? c.decodeIfPresent(AnalyticsLedgerMeta.self, forKey: .meta)) ?? AnalyticsLedgerMeta()
        totals = (try? c.decodeIfPresent(AnalyticsLedgerTotals.self, forKey: .totals)) ?? AnalyticsLedgerTotals()
        spendByDay = (try? c.decodeIfPresent([AnalyticsSpendDay].self, forKey: .spendByDay)) ?? []
        spendByAgent = (try? c.decodeIfPresent([AnalyticsAgentSpend].self, forKey: .spendByAgent)) ?? []
        spendByProject = (try? c.decodeIfPresent([AnalyticsProjectSpend].self, forKey: .spendByProject)) ?? []
        coverage = (try? c.decodeIfPresent(AnalyticsLedgerCoverage.self, forKey: .coverage)) ?? AnalyticsLedgerCoverage()
    }
}

// MARK: - Spec Score

public struct AnalyticsSpecScoreMeta: Equatable, Sendable {
    public let range: String
    public let fromIso: String
    public let toIso: String
    public let timezone: String
    public let generatedAtIso: String?
    public let scorerVersion: String?
    public let llmScorerVersion: String?

    public init(range: String = "30d", fromIso: String = "", toIso: String = "",
                timezone: String = "UTC", generatedAtIso: String? = nil,
                scorerVersion: String? = nil, llmScorerVersion: String? = nil) {
        self.range = range
        self.fromIso = fromIso
        self.toIso = toIso
        self.timezone = timezone
        self.generatedAtIso = generatedAtIso
        self.scorerVersion = scorerVersion
        self.llmScorerVersion = llmScorerVersion
    }
}

extension AnalyticsSpecScoreMeta: Decodable {
    private enum CodingKeys: String, CodingKey {
        case range, fromIso, toIso, timezone, generatedAtIso, scorerVersion, llmScorerVersion
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        range = (try? c.decodeIfPresent(String.self, forKey: .range)) ?? "30d"
        fromIso = (try? c.decodeIfPresent(String.self, forKey: .fromIso)) ?? ""
        toIso = (try? c.decodeIfPresent(String.self, forKey: .toIso)) ?? ""
        timezone = (try? c.decodeIfPresent(String.self, forKey: .timezone)) ?? "UTC"
        generatedAtIso = try? c.decodeIfPresent(String.self, forKey: .generatedAtIso)
        scorerVersion = try? c.decodeIfPresent(String.self, forKey: .scorerVersion)
        llmScorerVersion = try? c.decodeIfPresent(String.self, forKey: .llmScorerVersion)
    }
}

public struct AnalyticsStartRate: Equatable, Sendable {
    /// passing / sessionsWithFirstPrompt, 4dp fraction; null when denominator 0.
    public let rate: Double?
    public let passingSessions: Int
    public let sessionsWithFirstPrompt: Int
    public let totalScored: Int

    public init(rate: Double? = nil, passingSessions: Int = 0,
                sessionsWithFirstPrompt: Int = 0, totalScored: Int = 0) {
        self.rate = rate
        self.passingSessions = passingSessions
        self.sessionsWithFirstPrompt = sessionsWithFirstPrompt
        self.totalScored = totalScored
    }
}

extension AnalyticsStartRate: Decodable {
    private enum CodingKeys: String, CodingKey { case rate, passingSessions, sessionsWithFirstPrompt, totalScored }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        rate = c.flexDouble(.rate)
        passingSessions = c.flexInt(.passingSessions) ?? 0
        sessionsWithFirstPrompt = c.flexInt(.sessionsWithFirstPrompt) ?? 0
        totalScored = c.flexInt(.totalScored) ?? 0
    }
}

/// One day of the start-rate trend. Only days with scored rows appear.
public struct AnalyticsStartRateDay: Equatable, Sendable, Identifiable {
    public let date: String
    public let rate: Double?
    public let passingSessions: Int
    public let sessionsWithFirstPrompt: Int

    public var id: String { date }
    public var dayDate: Date? { AnalyticsDay.parse(date) }

    public init(date: String, rate: Double? = nil, passingSessions: Int = 0, sessionsWithFirstPrompt: Int = 0) {
        self.date = date
        self.rate = rate
        self.passingSessions = passingSessions
        self.sessionsWithFirstPrompt = sessionsWithFirstPrompt
    }
}

extension AnalyticsStartRateDay: Decodable {
    private enum CodingKeys: String, CodingKey { case date, rate, passingSessions, sessionsWithFirstPrompt }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        date = (try? c.decodeIfPresent(String.self, forKey: .date)) ?? ""
        rate = c.flexDouble(.rate)
        passingSessions = c.flexInt(.passingSessions) ?? 0
        sessionsWithFirstPrompt = c.flexInt(.sessionsWithFirstPrompt) ?? 0
    }
}

/// Always all five of A, B, C, D, F in order, zero-filled.
public struct AnalyticsGradeCount: Equatable, Sendable, Identifiable {
    public let grade: String
    public let sessionCount: Int

    public var id: String { grade }

    public init(grade: String, sessionCount: Int = 0) {
        self.grade = grade
        self.sessionCount = sessionCount
    }
}

extension AnalyticsGradeCount: Decodable {
    private enum CodingKeys: String, CodingKey { case grade, sessionCount }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        grade = (try? c.decodeIfPresent(String.self, forKey: .grade)) ?? ""
        sessionCount = c.flexInt(.sessionCount) ?? 0
    }
}

public struct AnalyticsDimensionRate: Equatable, Sendable, Identifiable {
    /// constraints | success_criteria | verification | context | specificity.
    public let dimension: String
    public let passCount: Int
    public let passRate: Double?

    public var id: String { dimension }

    public init(dimension: String, passCount: Int = 0, passRate: Double? = nil) {
        self.dimension = dimension
        self.passCount = passCount
        self.passRate = passRate
    }
}

extension AnalyticsDimensionRate: Decodable {
    private enum CodingKeys: String, CodingKey { case dimension, passCount, passRate }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        dimension = (try? c.decodeIfPresent(String.self, forKey: .dimension)) ?? ""
        passCount = c.flexInt(.passCount) ?? 0
        passRate = c.flexDouble(.passRate)
    }
}

/// One sub-check of an LLM judge dimension, with quoted evidence.
public struct SpecScoreCheck: Equatable, Sendable {
    public let id: String
    public let answer: Bool
    public let evidence: String?

    public init(id: String, answer: Bool = false, evidence: String? = nil) {
        self.id = id
        self.answer = answer
        self.evidence = evidence
    }
}

extension SpecScoreCheck: Decodable {
    private enum CodingKeys: String, CodingKey { case id, answer, evidence }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = (try? c.decodeIfPresent(String.self, forKey: .id)) ?? ""
        answer = (try? c.decodeIfPresent(Bool.self, forKey: .answer)) ?? false
        evidence = try? c.decodeIfPresent(String.self, forKey: .evidence)
    }
}

public struct SpecScoreDimEvidence: Equatable, Sendable {
    public let checks: [SpecScoreCheck]
    public let assessment: String?

    public init(checks: [SpecScoreCheck] = [], assessment: String? = nil) {
        self.checks = checks
        self.assessment = assessment
    }
}

extension SpecScoreDimEvidence: Decodable {
    private enum CodingKeys: String, CodingKey { case checks, assessment }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        checks = (try? c.decodeIfPresent([SpecScoreCheck].self, forKey: .checks)) ?? []
        assessment = try? c.decodeIfPresent(String.self, forKey: .assessment)
    }
}

/// One scored opener sample (exemplar or offender).
public struct SpecScoreSample: Equatable, Sendable, Identifiable {
    public let sessionId: String
    public let clientId: String
    public let score: Int
    public let grade: String
    /// "llm" | "rubric".
    public let scoredBy: String
    public let rationale: String?
    /// implementation | exploration | trivial-directive | null.
    public let llmIntent: String?
    public let dimEvidence: [String: SpecScoreDimEvidence]?
    public let duplicateCount: Int
    public let agentName: String
    public let projectId: String?
    public let firstPromptExcerpt: String?
    public let missingDims: [String]
    public let bucketIso: String?

    public var id: String { sessionId }
    public var bucketDate: Date? { CloudDate.parse(bucketIso) }

    public init(sessionId: String, clientId: String = "", score: Int = 0, grade: String = "F",
                scoredBy: String = "rubric", rationale: String? = nil, llmIntent: String? = nil,
                dimEvidence: [String: SpecScoreDimEvidence]? = nil, duplicateCount: Int = 1,
                agentName: String = "Unknown", projectId: String? = nil,
                firstPromptExcerpt: String? = nil, missingDims: [String] = [],
                bucketIso: String? = nil) {
        self.sessionId = sessionId
        self.clientId = clientId
        self.score = score
        self.grade = grade
        self.scoredBy = scoredBy
        self.rationale = rationale
        self.llmIntent = llmIntent
        self.dimEvidence = dimEvidence
        self.duplicateCount = duplicateCount
        self.agentName = agentName
        self.projectId = projectId
        self.firstPromptExcerpt = firstPromptExcerpt
        self.missingDims = missingDims
        self.bucketIso = bucketIso
    }
}

extension SpecScoreSample: Decodable {
    private enum CodingKeys: String, CodingKey {
        case sessionId, clientId, score, grade, scoredBy, rationale, llmIntent
        case dimEvidence, duplicateCount, agentName, projectId, firstPromptExcerpt
        case missingDims, bucketIso
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionId = (try? c.decodeIfPresent(String.self, forKey: .sessionId)) ?? ""
        clientId = (try? c.decodeIfPresent(String.self, forKey: .clientId)) ?? ""
        score = c.flexInt(.score) ?? 0
        grade = (try? c.decodeIfPresent(String.self, forKey: .grade)) ?? "F"
        scoredBy = (try? c.decodeIfPresent(String.self, forKey: .scoredBy)) ?? "rubric"
        rationale = try? c.decodeIfPresent(String.self, forKey: .rationale)
        llmIntent = try? c.decodeIfPresent(String.self, forKey: .llmIntent)
        dimEvidence = try? c.decodeIfPresent([String: SpecScoreDimEvidence].self, forKey: .dimEvidence)
        duplicateCount = c.flexInt(.duplicateCount) ?? 1
        agentName = (try? c.decodeIfPresent(String.self, forKey: .agentName)) ?? "Unknown"
        projectId = try? c.decodeIfPresent(String.self, forKey: .projectId)
        firstPromptExcerpt = try? c.decodeIfPresent(String.self, forKey: .firstPromptExcerpt)
        missingDims = (try? c.decodeIfPresent([String].self, forKey: .missingDims)) ?? []
        bucketIso = try? c.decodeIfPresent(String.self, forKey: .bucketIso)
    }
}

/// `GET /api/v1/analytics/spec-score` payload (`data.specScore`).
public struct AnalyticsSpecScore: Equatable, Sendable {
    public let meta: AnalyticsSpecScoreMeta
    public let startRate: AnalyticsStartRate
    public let startRateTrend: [AnalyticsStartRateDay]
    public let gradeDistribution: [AnalyticsGradeCount]
    public let perDimension: [AnalyticsDimensionRate]
    public let offendingSamples: [SpecScoreSample]
    public let exemplarSamples: [SpecScoreSample]

    public init(meta: AnalyticsSpecScoreMeta = AnalyticsSpecScoreMeta(),
                startRate: AnalyticsStartRate = AnalyticsStartRate(),
                startRateTrend: [AnalyticsStartRateDay] = [],
                gradeDistribution: [AnalyticsGradeCount] = [],
                perDimension: [AnalyticsDimensionRate] = [],
                offendingSamples: [SpecScoreSample] = [],
                exemplarSamples: [SpecScoreSample] = []) {
        self.meta = meta
        self.startRate = startRate
        self.startRateTrend = startRateTrend
        self.gradeDistribution = gradeDistribution
        self.perDimension = perDimension
        self.offendingSamples = offendingSamples
        self.exemplarSamples = exemplarSamples
    }

    public var isEmpty: Bool { startRate.totalScored == 0 }

    /// Count-weighted letter grade across the distribution (A=4 .. F=0,
    /// weighted mean, rounded, mapped back). Nil when nothing is scored.
    public var aggregateGrade: String? {
        Self.aggregateGrade(from: gradeDistribution)
    }

    public static func aggregateGrade(from distribution: [AnalyticsGradeCount]) -> String? {
        let points: [String: Double] = ["A": 4, "B": 3, "C": 2, "D": 1, "F": 0]
        var weighted = 0.0
        var total = 0
        for entry in distribution {
            guard let value = points[entry.grade.uppercased()], entry.sessionCount > 0 else { continue }
            weighted += value * Double(entry.sessionCount)
            total += entry.sessionCount
        }
        guard total > 0 else { return nil }
        let letters = ["F", "D", "C", "B", "A"]
        let index = Int((weighted / Double(total)).rounded())
        return letters[max(0, min(letters.count - 1, index))]
    }
}

extension AnalyticsSpecScore: Decodable {
    private enum CodingKeys: String, CodingKey {
        case meta, startRate, startRateTrend, gradeDistribution, perDimension
        case offendingSamples, exemplarSamples
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        meta = (try? c.decodeIfPresent(AnalyticsSpecScoreMeta.self, forKey: .meta)) ?? AnalyticsSpecScoreMeta()
        startRate = (try? c.decodeIfPresent(AnalyticsStartRate.self, forKey: .startRate)) ?? AnalyticsStartRate()
        startRateTrend = (try? c.decodeIfPresent([AnalyticsStartRateDay].self, forKey: .startRateTrend)) ?? []
        gradeDistribution = (try? c.decodeIfPresent([AnalyticsGradeCount].self, forKey: .gradeDistribution)) ?? []
        perDimension = (try? c.decodeIfPresent([AnalyticsDimensionRate].self, forKey: .perDimension)) ?? []
        offendingSamples = (try? c.decodeIfPresent([SpecScoreSample].self, forKey: .offendingSamples)) ?? []
        exemplarSamples = (try? c.decodeIfPresent([SpecScoreSample].self, forKey: .exemplarSamples)) ?? []
    }
}

/// `GET /api/v1/analytics/spec-score/sessions` payload (`data.specScoreSessions`).
public struct SpecScoreSessionsPage: Equatable, Sendable {
    public let range: String
    public let grade: String
    public let total: Int
    public let offset: Int
    public let limit: Int
    public let sessions: [SpecScoreSample]

    public init(range: String = "30d", grade: String = "A", total: Int = 0,
                offset: Int = 0, limit: Int = 25, sessions: [SpecScoreSample] = []) {
        self.range = range
        self.grade = grade
        self.total = total
        self.offset = offset
        self.limit = limit
        self.sessions = sessions
    }
}

extension SpecScoreSessionsPage: Decodable {
    private enum CodingKeys: String, CodingKey { case range, grade, total, offset, limit, sessions }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        range = (try? c.decodeIfPresent(String.self, forKey: .range)) ?? "30d"
        grade = (try? c.decodeIfPresent(String.self, forKey: .grade)) ?? ""
        total = c.flexInt(.total) ?? 0
        offset = c.flexInt(.offset) ?? 0
        limit = c.flexInt(.limit) ?? 25
        sessions = (try? c.decodeIfPresent([SpecScoreSample].self, forKey: .sessions)) ?? []
    }
}

// MARK: - Processing

/// `GET /api/v1/analytics/processing` payload (`data.processing`). Cheap poll:
/// clients poll ~12s while pending > 0, then refetch everything once on the
/// pending -> 0 edge.
public struct AnalyticsProcessing: Equatable, Sendable {
    public let pending: Int
    public let stalled: Int
    public let derivedThroughIso: String?
    public let generatedAtIso: String?

    public var derivedThroughDate: Date? { CloudDate.parse(derivedThroughIso) }

    public init(pending: Int = 0, stalled: Int = 0, derivedThroughIso: String? = nil, generatedAtIso: String? = nil) {
        self.pending = pending
        self.stalled = stalled
        self.derivedThroughIso = derivedThroughIso
        self.generatedAtIso = generatedAtIso
    }
}

extension AnalyticsProcessing: Decodable {
    private enum CodingKeys: String, CodingKey { case pending, stalled, derivedThroughIso, generatedAtIso }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pending = c.flexInt(.pending) ?? 0
        stalled = c.flexInt(.stalled) ?? 0
        derivedThroughIso = try? c.decodeIfPresent(String.self, forKey: .derivedThroughIso)
        generatedAtIso = try? c.decodeIfPresent(String.self, forKey: .generatedAtIso)
    }
}
