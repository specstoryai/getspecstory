import Charts
import SwiftUI
import SpecStoryKit

/// Activity tab: synced sessions over time (stacked by agent) plus the
/// past-year Active days section with streak tiles and a contribution grid.
struct AnalyticsActivityTab: View {
    let overview: AnalyticsOverview

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if overview.isEmpty && overview.activityCalendar.allSatisfy({ $0.count == 0 }) {
                emptyState
            } else {
                sessionsOverTime
                activeDays
            }
        }
    }

    // MARK: - Synced sessions over time

    private var sessionsOverTime: some View {
        let agents = AgentDaySeries.orderedAgents(overview.sessionsByDayByAgent)
        let marks = AgentDaySeries.marks(overview.sessionsByDayByAgent, agents: agents)

        return AnalyticsCard(title: "Synced sessions over time", subtitle: "Sessions per UTC day, stacked by agent", action: {
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
                    AxisMarks(values: .stride(by: .day, count: max(1, overview.sessionsByDayByAgent.count / 5))) { _ in
                        AxisValueLabel(format: .dateTime.month(.abbreviated).day())
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                .chartYAxis {
                    AxisMarks(values: .automatic(desiredCount: 5)) { _ in
                        AxisGridLine().foregroundStyle(Theme.hairline)
                        AxisValueLabel()
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                .frame(height: 240)
            }
        }
    }

    // MARK: - Active days (trailing year, derived from the 365-cell calendar)

    private struct YearStats {
        var activeDays = 0
        var currentStreak = 0
        var longestStreak = 0
    }

    /// Client-side year stats from the zero-filled trailing-year calendar,
    /// matching the web tab (which ignores the window-scoped overview.activity
    /// here on purpose: this section describes the past year).
    private var yearStats: YearStats {
        var stats = YearStats()
        var run = 0
        for day in overview.activityCalendar {
            if day.count > 0 {
                stats.activeDays += 1
                run += 1
                stats.longestStreak = max(stats.longestStreak, run)
            } else {
                run = 0
            }
        }
        // Current streak walks back from the newest day; an inactive today
        // still counts yesterday's run.
        var current = 0
        var index = overview.activityCalendar.count - 1
        if index >= 0 && overview.activityCalendar[index].count == 0 {
            index -= 1
        }
        while index >= 0, overview.activityCalendar[index].count > 0 {
            current += 1
            index -= 1
        }
        stats.currentStreak = current
        return stats
    }

    private var activeDays: some View {
        let stats = yearStats

        return AnalyticsCard(title: "Active days", subtitle: "Past year of synced session activity (UTC)") {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 10) {
                    miniStat(value: "\(stats.activeDays)", label: "Active days")
                    miniStat(value: "\(stats.currentStreak)d", label: "Current streak")
                    miniStat(value: "\(stats.longestStreak)d", label: "Longest streak")
                }
                if overview.activityCalendar.isEmpty {
                    AnalyticsEmptyHint(text: "No activity synced yet.")
                } else {
                    calendarHeatmap
                }
            }
        }
    }

    private func miniStat(value: String, label: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(value)
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
                .monospacedDigit()
            Text(label)
                .font(Theme.body(10, weight: .medium))
                .foregroundStyle(Theme.inkTertiary)
                .textCase(.uppercase)
                .kerning(0.3)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(Theme.paper)
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .strokeBorder(Theme.hairline, lineWidth: 1)
        )
    }

    // MARK: - Heatmap

    /// GitHub-style contribution grid: weeks as columns, weekdays as rows.
    private var calendarHeatmap: some View {
        let weeks = heatmapWeeks
        let maxCount = max(1, overview.activityCalendar.map(\.count).max() ?? 1)

        return VStack(alignment: .leading, spacing: 4) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(alignment: .top, spacing: 2) {
                    ForEach(Array(weeks.enumerated()), id: \.offset) { _, week in
                        VStack(spacing: 2) {
                            ForEach(Array(week.enumerated()), id: \.offset) { _, day in
                                RoundedRectangle(cornerRadius: 1.5, style: .continuous)
                                    .fill(heatTint(count: day?.count, max: maxCount))
                                    .frame(width: 8, height: 8)
                                    .help(day.map { "\(AnalyticsFormat.dayLabel($0.date)) · \($0.count) session\($0.count == 1 ? "" : "s")" } ?? "")
                            }
                        }
                    }
                }
            }
            HStack(spacing: 4) {
                Spacer()
                Text("less")
                    .font(Theme.body(9))
                    .foregroundStyle(Theme.inkTertiary)
                ForEach(0..<5, id: \.self) { step in
                    RoundedRectangle(cornerRadius: 1.5, style: .continuous)
                        .fill(step == 0 ? Theme.hairline : Theme.accent.opacity([0.2, 0.4, 0.6, 0.9][step - 1]))
                        .frame(width: 8, height: 8)
                }
                Text("more")
                    .font(Theme.body(9))
                    .foregroundStyle(Theme.inkTertiary)
            }
        }
    }

    /// Buckets the trailing-year days into week columns, padding the first
    /// week so weekday rows line up (0 = Sunday).
    private var heatmapWeeks: [[AnalyticsCalendarDay?]] {
        let days = overview.activityCalendar
        guard !days.isEmpty else { return [] }

        var utcCalendar = Calendar(identifier: .gregorian)
        utcCalendar.timeZone = TimeZone(identifier: "UTC") ?? .current

        var weeks: [[AnalyticsCalendarDay?]] = []
        var current: [AnalyticsCalendarDay?] = []
        if let firstDate = days.first?.dayDate {
            let weekday = utcCalendar.component(.weekday, from: firstDate) - 1  // 0 = Sunday
            current = Array(repeating: nil, count: weekday)
        }
        for day in days {
            current.append(day)
            if current.count == 7 {
                weeks.append(current)
                current = []
            }
        }
        if !current.isEmpty {
            current.append(contentsOf: Array(repeating: nil, count: 7 - current.count))
            weeks.append(current)
        }
        return weeks
    }

    private func heatTint(count: Int?, max maxCount: Int) -> Color {
        guard let count else { return .clear }
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
            Image(systemName: "calendar")
                .font(.system(size: 34, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text("No activity in this window")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("Synced sessions land here as day-by-day activity, streaks, and a year of history.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 90)
    }
}
