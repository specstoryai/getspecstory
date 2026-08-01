import Charts
import SwiftUI
import SpecStoryKit

/// Messages tab: exchange tiles plus the Spec Score section (explainer,
/// grade tiles, grade distribution, spec-driven start over time).
struct AnalyticsMessagesTab: View {
    let overview: AnalyticsOverview
    let specScore: AnalyticsSpecScore?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if overview.isEmpty {
                emptyState
            } else {
                tilesRow
                specScoreSection
            }
        }
    }

    // MARK: - Exchange tiles

    private var tilesRow: some View {
        HStack(alignment: .top, spacing: 10) {
            AnalyticsTile(
                icon: "bubble.left.and.bubble.right",
                label: "Total exchanges",
                value: AnalyticsFormat.compact(overview.messages.totalExchanges),
                sublabel: "user and agent turns in window"
            )
            AnalyticsTile(
                icon: "square.stack.3d.up",
                label: "Avg per session",
                value: String(format: "%.1f", overview.messages.avgExchangesPerSession),
                sublabel: "turns / session"
            )
            AnalyticsTile(
                icon: "trophy",
                label: "Deepest session",
                value: overview.messages.longestSession.map { "\($0.exchangeCount)" } ?? "-",
                sublabel: overview.messages.longestSession.map {
                    "turns · \(AgentPalette.displayName($0.agentName))"
                } ?? "no sessions yet"
            )
        }
    }

    // MARK: - Spec Score

    @ViewBuilder
    private var specScoreSection: some View {
        sectionHeader

        explainerBanner

        if let specScore, !specScore.isEmpty {
            specTiles(specScore)
            gradeDistribution(specScore)
            startRateOverTime(specScore)
        } else {
            AnalyticsCard(title: "Spec Score") {
                AnalyticsEmptyHint(text: "No scored openers in this window yet. Scores appear shortly after sessions sync.")
            }
        }
    }

    private var sectionHeader: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Spec Score")
                .font(Theme.display(17))
                .foregroundStyle(Theme.ink)
            Text("How spec-driven your openers are. An AI-assessed grade of the first prompt in each session.")
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
        }
        .padding(.top, 6)
    }

    private var explainerBanner: some View {
        let excluded = specScore.map { max(0, $0.startRate.totalScored - $0.startRate.sessionsWithFirstPrompt) } ?? 0
        return AnalyticsInfoBanner(
            icon: "info.circle",
            lead: "A judgement of your opening prompt, never of your work.",
            text: "An AI judge grades each opener against a fixed rubric (goal, context, constraints, verification, sizing) and explains its grade; openers it hasn't assessed fall back to a deterministic keyword heuristic.",
            secondary: excluded > 0
                ? (excluded == 1
                    ? "1 session had no extractable opener and is excluded from the start rate."
                    : "\(excluded) sessions had no extractable opener and are excluded from the start rate.")
                : nil
        )
    }

    private func specTiles(_ specScore: AnalyticsSpecScore) -> some View {
        let withoutPrompt = max(0, specScore.startRate.totalScored - specScore.startRate.sessionsWithFirstPrompt)
        return HStack(alignment: .top, spacing: 10) {
            AnalyticsTile(
                icon: "checkmark.seal",
                label: "Spec-driven start",
                value: AnalyticsFormat.rate(specScore.startRate.rate),
                sublabel: "\(specScore.startRate.passingSessions)/\(specScore.startRate.sessionsWithFirstPrompt) openers",
                accent: true
            )
            AnalyticsTile(
                icon: "gauge.with.needle",
                label: "Typical grade",
                value: specScore.aggregateGrade ?? "-",
                sublabel: "count-weighted opener grade"
            )
            AnalyticsTile(
                icon: "target",
                label: "Scored sessions",
                value: AnalyticsFormat.compact(specScore.startRate.totalScored),
                sublabel: "\(withoutPrompt) had no opener"
            )
        }
    }

    private func gradeDistribution(_ specScore: AnalyticsSpecScore) -> some View {
        let grades = ["A", "B", "C", "D", "F"]
        let counts = Dictionary(specScore.gradeDistribution.map { ($0.grade.uppercased(), $0.sessionCount) },
                                uniquingKeysWith: { first, _ in first })

        return AnalyticsCard(title: "Grade distribution", subtitle: "Openers by letter grade") {
            if counts.values.reduce(0, +) == 0 {
                AnalyticsEmptyHint(text: "No graded openers in this window.")
            } else {
                Chart(grades, id: \.self) { grade in
                    BarMark(
                        x: .value("Grade", grade),
                        y: .value("Sessions", counts[grade] ?? 0),
                        width: .ratio(0.55)
                    )
                    .foregroundStyle(GradeStyle.barColor(grade))
                    .cornerRadius(3)
                    .annotation(position: .top, alignment: .center, spacing: 4) {
                        let count = counts[grade] ?? 0
                        if count > 0 {
                            Text(count.formatted())
                                .font(Theme.body(9))
                                .foregroundStyle(Theme.inkTertiary)
                                .monospacedDigit()
                        }
                    }
                }
                .chartXAxis {
                    AxisMarks { _ in
                        AxisValueLabel()
                            .font(Theme.body(11, weight: .medium))
                            .foregroundStyle(Theme.inkSecondary)
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

    private struct TrendPoint: Identifiable {
        let date: Date
        let rate: Double
        var id: Date { date }
    }

    private func startRateOverTime(_ specScore: AnalyticsSpecScore) -> some View {
        let points: [TrendPoint] = specScore.startRateTrend.compactMap { day in
            guard let date = day.dayDate, day.sessionsWithFirstPrompt > 0, let rate = day.rate else { return nil }
            return TrendPoint(date: date, rate: rate * 100)
        }

        return AnalyticsCard(title: "Spec-driven start over time", subtitle: "Share of openers that pass, per day") {
            if points.isEmpty {
                AnalyticsEmptyHint(text: "No days with scorable openers yet.")
            } else {
                Chart(points) { point in
                    AreaMark(
                        x: .value("Day", point.date, unit: .day),
                        y: .value("Rate", point.rate)
                    )
                    .foregroundStyle(
                        LinearGradient(colors: [Theme.accent.opacity(0.22), Theme.accent.opacity(0.02)],
                                       startPoint: .top, endPoint: .bottom)
                    )
                    .interpolationMethod(.monotone)
                    LineMark(
                        x: .value("Day", point.date, unit: .day),
                        y: .value("Rate", point.rate)
                    )
                    .foregroundStyle(Theme.accent)
                    .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
                    .interpolationMethod(.monotone)
                }
                .chartYScale(domain: 0...100)
                .chartXAxis {
                    AxisMarks(values: .automatic(desiredCount: 5)) { _ in
                        AxisValueLabel(format: .dateTime.month(.abbreviated).day())
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                .chartYAxis {
                    AxisMarks(values: [0, 25, 50, 75, 100]) { value in
                        AxisGridLine().foregroundStyle(Theme.hairline)
                        AxisValueLabel {
                            if let rate = value.as(Int.self) {
                                Text("\(rate)%")
                                    .font(Theme.body(10))
                                    .foregroundStyle(Theme.inkTertiary)
                            }
                        }
                    }
                }
                .frame(height: 170)
            }
        }
    }

    // MARK: - Empty

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "bubble.left.and.bubble.right")
                .font(.system(size: 34, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text("No messages in this window")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("Conversation depth and Spec Score grades appear once sessions with messages sync.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 90)
    }
}
