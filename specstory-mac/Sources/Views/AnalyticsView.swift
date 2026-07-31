import Charts
import SwiftUI
import SpecStoryKit

/// Native analytics over the local session index: headline tiles, sessions
/// per day, per-agent split, top projects, and an hour-of-day histogram.
/// All data is aggregated in SQLite by AnalyticsQueries; this view only draws.
struct AnalyticsView: View {
    @ObservedObject var analytics: AnalyticsModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header

                if analytics.isEmpty {
                    if analytics.loading {
                        loadingState
                    } else {
                        emptyState
                    }
                } else {
                    tilesRow
                    sessionsPerDayCard
                    byAgentCard
                    topProjectsCard
                    hourOfDayCard
                }
            }
            .padding(28)
            .frame(maxWidth: Theme.feedWidth, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .background(Theme.paper)
        .task {
            analytics.refresh()
        }
    }

    // MARK: - Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 10) {
                Text("Analytics")
                    .font(Theme.display(24))
                    .foregroundStyle(Theme.ink)
                if analytics.loading && !analytics.isEmpty {
                    ProgressView().controlSize(.small)
                }
            }
            Text("Your coding activity across every agent on this Mac.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
        }
        .padding(.bottom, 4)
    }

    // MARK: - Stat tiles

    private var tilesRow: some View {
        HStack(spacing: 10) {
            StatTile(label: "Sessions", value: analytics.snapshot.sessions.formatted())
            StatTile(label: "Projects", value: analytics.snapshot.projects.formatted())
            StatTile(label: "Prompts", value: analytics.snapshot.userTurns.formatted())
            StatTile(label: "Day streak", value: analytics.snapshot.streak.formatted())
        }
    }

    // MARK: - Sessions per day

    private var sessionsPerDayCard: some View {
        chartCard(title: "Sessions per day", caption: "Last 30 days") {
            Chart(analytics.snapshot.perDay) { day in
                BarMark(
                    x: .value("Day", day.date, unit: .day),
                    y: .value("Sessions", day.count),
                    width: .ratio(0.6)
                )
                .foregroundStyle(Theme.accent)
                .cornerRadius(3)
            }
            .chartXAxis {
                AxisMarks(values: .stride(by: .day, count: 7)) { _ in
                    AxisGridLine().foregroundStyle(Theme.hairline)
                    AxisValueLabel(format: .dateTime.weekday(.abbreviated).day())
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
            .chartYScale(domain: 0...maxPerDayDomain)
            .frame(height: 180)
        }
    }

    private var maxPerDayDomain: Int {
        max(4, analytics.snapshot.perDay.map(\.count).max() ?? 0)
    }

    // MARK: - By agent

    private var byAgentCard: some View {
        chartCard(title: "By agent", caption: nil) {
            VStack(alignment: .leading, spacing: 12) {
                ForEach(analytics.snapshot.providers) { stat in
                    providerRow(stat)
                }
            }
        }
    }

    private func providerRow(_ stat: AnalyticsProviderStat) -> some View {
        let provider = Provider(providerID: stat.provider)
        let color = provider?.badgeColor ?? Theme.inkSecondary
        let maxSessions = analytics.snapshot.providers.map(\.sessions).max() ?? 1
        let fraction = maxSessions > 0 ? Double(stat.sessions) / Double(maxSessions) : 0

        return HStack(spacing: 10) {
            ProviderIcon(provider: provider, fallbackName: stat.provider, size: 24)

            Text(provider?.displayName ?? stat.provider.capitalized)
                .font(Theme.body(12, weight: .medium))
                .foregroundStyle(Theme.ink)
                .lineLimit(1)
                .frame(width: 108, alignment: .leading)

            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(Theme.hairline)
                    Capsule()
                        .fill(color)
                        .frame(width: max(6, geo.size.width * fraction))
                }
            }
            .frame(height: 6)

            Text(sessionsAndPrompts(sessions: stat.sessions, prompts: stat.userTurns))
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
                .monospacedDigit()
                .lineLimit(1)
                .fixedSize()
        }
    }

    private func sessionsAndPrompts(sessions: Int, prompts: Int) -> String {
        let sessionPart = "\(sessions.formatted()) session\(sessions == 1 ? "" : "s")"
        let promptPart = "\(prompts.formatted()) prompt\(prompts == 1 ? "" : "s")"
        return "\(sessionPart) · \(promptPart)"
    }

    // MARK: - Top projects

    private var topProjectsCard: some View {
        chartCard(title: "Top projects", caption: nil) {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(analytics.snapshot.topProjects.enumerated()), id: \.element.id) { index, project in
                    if index > 0 {
                        Divider().overlay(Theme.hairline)
                    }
                    projectRow(project)
                }
            }
        }
    }

    private func projectRow(_ project: AnalyticsProjectStat) -> some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(displayName(for: project))
                    .font(Theme.body(12, weight: .medium))
                    .foregroundStyle(Theme.ink)
                    .lineLimit(1)
                Text(sessionsAndPrompts(sessions: project.sessions, prompts: project.userTurns))
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkSecondary)
                    .monospacedDigit()
            }
            Spacer()
            if let lastActivity = project.lastActivity {
                Text(Self.relativeFormatter.localizedString(for: lastActivity, relativeTo: Date()))
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkTertiary)
            }
        }
        .padding(.vertical, 9)
    }

    private func displayName(for project: AnalyticsProjectStat) -> String {
        if project.projectID == "unknown" || project.name.isEmpty {
            return "Unattributed"
        }
        return project.name
    }

    // MARK: - Hour of day

    private var hourOfDayCard: some View {
        chartCard(title: "Hour of day", caption: peakHourCaption) {
            Chart(hourBuckets) { bucket in
                BarMark(
                    x: .value("Hour", Double(bucket.hour)),
                    y: .value("Sessions", bucket.count),
                    width: .ratio(0.55)
                )
                .foregroundStyle(bucket.hour == peakHour ? Theme.accent : Theme.accent.opacity(0.35))
                .cornerRadius(2)
            }
            .chartXScale(domain: -0.5...23.5)
            .chartXAxis {
                AxisMarks(values: [0, 6, 12, 18]) { value in
                    AxisValueLabel {
                        if let hour = value.as(Int.self) {
                            Text(Self.hourLabel(hour))
                                .font(Theme.body(10))
                                .foregroundStyle(Theme.inkTertiary)
                        }
                    }
                }
            }
            .chartYAxis(.hidden)
            .frame(height: 90)
        }
    }

    private struct HourBucket: Identifiable {
        let hour: Int
        let count: Int
        var id: Int { hour }
    }

    private var hourBuckets: [HourBucket] {
        analytics.snapshot.hourHistogram.enumerated().map { HourBucket(hour: $0.offset, count: $0.element) }
    }

    private var peakHour: Int? {
        let histogram = analytics.snapshot.hourHistogram
        guard let maxCount = histogram.max(), maxCount > 0 else { return nil }
        return histogram.firstIndex(of: maxCount)
    }

    private var peakHourCaption: String? {
        guard let peakHour else { return nil }
        return "Busiest around \(Self.hourLabel(peakHour))"
    }

    private static func hourLabel(_ hour: Int) -> String {
        let twelve = hour % 12 == 0 ? 12 : hour % 12
        return "\(twelve)\(hour < 12 ? "am" : "pm")"
    }

    // MARK: - Card scaffold

    private func chartCard(title: String, caption: String?, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(title)
                    .font(Theme.body(12, weight: .semibold))
                    .foregroundStyle(Theme.inkTertiary)
                    .textCase(.uppercase)
                    .kerning(0.4)
                Spacer()
                if let caption {
                    Text(caption)
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                }
            }
            content()
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }

    // MARK: - Empty and loading states

    private var loadingState: some View {
        VStack(spacing: 10) {
            ProgressView()
            Text("Crunching your sessions")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 120)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "chart.bar")
                .font(.system(size: 34, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text("No activity to chart yet")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("Analytics build up as SpecStory indexes your sessions. Start a session in any supported agent and check back here.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 110)
    }

    private static let relativeFormatter: RelativeDateTimeFormatter = {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter
    }()
}
