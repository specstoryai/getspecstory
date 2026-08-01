import Charts
import SwiftUI
import SpecStoryKit

/// Overview tab: four hero tiles, Month at a glance (stacked by agent),
/// Highlights (window vs previous), and the When-you-code punchcard.
struct AnalyticsOverviewTab: View {
    let overview: AnalyticsOverview
    let ledger: AnalyticsLedger?
    let specScore: AnalyticsSpecScore?
    let openLedger: () -> Void
    let openActivity: () -> Void
    let openMessages: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if overview.isEmpty {
                emptyState
            } else {
                tilesRow
                monthAtAGlance
                highlights
                whenYouCode
            }
        }
    }

    // MARK: - Hero tiles

    private var tilesRow: some View {
        HStack(alignment: .top, spacing: 10) {
            specDrivenTile
            streakTile
            messagesTile
            spendTile
        }
    }

    /// Prefers the real spec-score start rate; the overview's embedded v0
    /// heuristic covers the gap while that payload loads.
    private var specDrivenTile: some View {
        let rate = specScore?.startRate.rate ?? overview.specDrivenStart.rate
        let passing = specScore?.startRate.passingSessions ?? overview.specDrivenStart.passingSessions
        let denom = specScore?.startRate.sessionsWithFirstPrompt ?? overview.specDrivenStart.sessionsWithFirstPrompt
        let grade = specScore.flatMap(\.aggregateGrade)
        let delta = overview.specDrivenStart.trendDelta

        return Button(action: openMessages) {
            AnalyticsTile(
                icon: "checkmark.seal",
                label: "Spec-driven start",
                value: AnalyticsFormat.rate(rate),
                sublabel: denom > 0 ? "\(passing)/\(denom) sessions" : "no prompts yet",
                accent: true
            ) {
                HStack {
                    if let grade {
                        GradeChip(grade: grade)
                    } else {
                        Text("estimated")
                            .font(Theme.body(9, weight: .medium))
                            .foregroundStyle(Theme.inkTertiary)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Theme.hairline)
                            .clipShape(Capsule())
                    }
                    Spacer()
                    if let delta {
                        TrendBadge(
                            direction: delta > 0 ? "up" : (delta < 0 ? "down" : "flat"),
                            text: AnalyticsFormat.deltaPct(delta) ?? "0%"
                        )
                    } else {
                        TrendBadge(direction: "flat", text: "new")
                    }
                }
            }
        }
        .buttonStyle(.tactile)
    }

    private var streakTile: some View {
        Button(action: openActivity) {
            AnalyticsTile(
                icon: "flame",
                label: "Current streak",
                value: "\(overview.activity.currentStreak)d",
                sublabel: "\(overview.activity.activeDays) active days"
            ) {
                Text("Longest: \(overview.activity.longestStreak)d")
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
            }
        }
        .buttonStyle(.tactile)
    }

    private var messagesTile: some View {
        Button(action: openMessages) {
            AnalyticsTile(
                icon: "bubble.left.and.bubble.right",
                label: "Messages",
                value: AnalyticsFormat.compact(overview.messages.totalExchanges),
                sublabel: String(format: "%.1f avg / session", overview.messages.avgExchangesPerSession)
            ) {
                if let longest = overview.messages.longestSession {
                    Text("Deepest: \(longest.exchangeCount) turns")
                        .font(Theme.body(10))
                        .foregroundStyle(Theme.inkTertiary)
                }
            }
        }
        .buttonStyle(.tactile)
    }

    private var spendTile: some View {
        Button(action: openLedger) {
            AnalyticsTile(
                icon: "dollarsign.circle",
                label: "Estimated spend",
                value: ledger.map { AnalyticsFormat.usd($0.totals.estCostUsd) } ?? "-",
                sublabel: "estimated · not a bill"
            ) {
                HStack(spacing: 3) {
                    Text("Open ledger")
                    Image(systemName: "arrow.right")
                        .font(.system(size: 8, weight: .semibold))
                }
                .font(Theme.body(10, weight: .medium))
                .foregroundStyle(Theme.accent)
            }
        }
        .buttonStyle(.tactile)
    }

    // MARK: - Month at a glance

    private var agents: [String] { AgentDaySeries.orderedAgents(overview.sessionsByDayByAgent) }

    private var monthAtAGlance: some View {
        let agents = agents
        let marks = AgentDaySeries.marks(overview.sessionsByDayByAgent, agents: agents)

        return AnalyticsCard(title: "Month at a glance", subtitle: "Sessions per day, stacked by agent", action: {
            AgentLegend(agents: Array(agents.prefix(4)))
        }) {
            if marks.isEmpty {
                AnalyticsEmptyHint(text: "No time-bucketable sessions in this window.")
            } else {
                Chart(marks) { mark in
                    BarMark(
                        x: .value("Day", mark.day, unit: .day),
                        y: .value("Sessions", mark.count),
                        width: .ratio(0.62)
                    )
                    .foregroundStyle(by: .value("Agent", mark.agent))
                    .cornerRadius(2)
                }
                .chartForegroundStyleScale(domain: agents, range: agents.map { AgentPalette.color(for: $0) })
                .chartLegend(.hidden)
                .chartXAxis {
                    AxisMarks(values: .stride(by: .day, count: max(1, overview.sessionsByDayByAgent.count / 4))) { _ in
                        AxisValueLabel(format: .dateTime.month(.abbreviated).day())
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                .chartYAxis {
                    AxisMarks(values: .automatic(desiredCount: 4)) { _ in
                        AxisGridLine().foregroundStyle(Theme.hairline)
                        AxisValueLabel()
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                .frame(height: 170)
            }
        }
    }

    // MARK: - Highlights

    private var highlights: some View {
        AnalyticsCard(title: "Highlights", subtitle: "This window vs the previous one") {
            if overview.highlights.isEmpty {
                AnalyticsEmptyHint(text: "Nothing to compare yet.")
            } else {
                HStack(alignment: .top, spacing: 0) {
                    ForEach(Array(overview.highlights.prefix(4).enumerated()), id: \.element.id) { index, highlight in
                        if index > 0 {
                            Divider().overlay(Theme.hairline).frame(height: 52)
                        }
                        highlightCell(highlight)
                    }
                }
            }
        }
    }

    private func highlightCell(_ highlight: AnalyticsHighlight) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(highlight.label)
                .font(Theme.body(10, weight: .medium))
                .foregroundStyle(Theme.inkTertiary)
                .textCase(.uppercase)
                .kerning(0.3)
            Text(AnalyticsFormat.compact(highlight.current))
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
                .monospacedDigit()
            HStack(spacing: 4) {
                TrendBadge(
                    direction: highlight.direction,
                    text: AnalyticsFormat.deltaPct(highlight.deltaPct) ?? AnalyticsFormat.deltaAbs(highlight.deltaAbs)
                )
                Text("from \(AnalyticsFormat.compact(highlight.previous))")
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
                    .monospacedDigit()
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 10)
    }

    // MARK: - When you code (punchcard)

    private static let weekdayLabels = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]

    private var timezoneShortLabel: String {
        overview.punchcard.timezone == "UTC"
            ? "UTC"
            : (TimeZone(identifier: overview.punchcard.timezone)?.abbreviation() ?? overview.punchcard.timezone)
    }

    private var whenYouCode: some View {
        AnalyticsCard(
            title: "When you code",
            subtitle: "Sessions by hour and weekday (\(timezoneShortLabel))",
            action: {
                if let peak = overview.punchcard.peakWindow {
                    HStack(spacing: 4) {
                        Image(systemName: "flame")
                            .font(.system(size: 9, weight: .semibold))
                        Text("Peak: \(peak.label) \(timezoneShortLabel) · \(peak.count)")
                            .font(Theme.body(10, weight: .medium))
                            .monospacedDigit()
                    }
                    .foregroundStyle(Theme.accent)
                }
            }
        ) {
            if overview.punchcard.maxCount == 0 {
                AnalyticsEmptyHint(text: "No time-bucketable sessions in this window.")
            } else {
                punchcardGrid
            }
        }
    }

    private var punchcardGrid: some View {
        let maxCount = max(1, overview.punchcard.maxCount)

        return VStack(alignment: .leading, spacing: 3) {
            ForEach(0..<7, id: \.self) { day in
                HStack(spacing: 3) {
                    Text(Self.weekdayLabels[day])
                        .font(Theme.body(9))
                        .foregroundStyle(Theme.inkTertiary)
                        .frame(width: 26, alignment: .trailing)
                    ForEach(0..<24, id: \.self) { hour in
                        let count = overview.punchcard.count(dayOfWeek: day, hour: hour)
                        RoundedRectangle(cornerRadius: 2, style: .continuous)
                            .fill(punchTint(count: count, max: maxCount))
                            .frame(maxWidth: .infinity)
                            .frame(height: 14)
                            .help("\(Self.weekdayLabels[day]) \(String(format: "%02d", hour)):00 · \(count) session\(count == 1 ? "" : "s")")
                    }
                }
            }
            HStack(spacing: 3) {
                Spacer().frame(width: 26)
                ForEach([0, 6, 12, 18, 23], id: \.self) { hour in
                    Text(String(format: "%02d", hour))
                        .font(Theme.body(9))
                        .foregroundStyle(Theme.inkTertiary)
                    if hour != 23 { Spacer() }
                }
            }
            HStack(spacing: 4) {
                Spacer()
                Text("less")
                    .font(Theme.body(9))
                    .foregroundStyle(Theme.inkTertiary)
                ForEach(0..<5, id: \.self) { step in
                    RoundedRectangle(cornerRadius: 2, style: .continuous)
                        .fill(step == 0 ? Theme.hairline : Theme.accent.opacity([0.2, 0.4, 0.6, 0.9][step - 1]))
                        .frame(width: 10, height: 10)
                }
                Text("more")
                    .font(Theme.body(9))
                    .foregroundStyle(Theme.inkTertiary)
            }
            .padding(.top, 2)
        }
    }

    /// Mirrors the web's intensityBucket thresholds 0.25/0.5/0.75.
    private func punchTint(count: Int, max maxCount: Int) -> Color {
        guard count > 0 else { return Theme.hairline }
        let fraction = Double(count) / Double(maxCount)
        if fraction <= 0.25 { return Theme.accent.opacity(0.2) }
        if fraction <= 0.5 { return Theme.accent.opacity(0.4) }
        if fraction <= 0.75 { return Theme.accent.opacity(0.6) }
        return Theme.accent.opacity(0.9)
    }

    // MARK: - Empty

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "bubble.left.and.bubble.right")
                .font(.system(size: 34, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text("No sessions in this window")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("We didn't find any coding sessions in this window. Start a session with any agent and it will show up here.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 90)
    }
}
