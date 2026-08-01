import Charts
import SwiftUI
import SpecStoryKit

/// Ledger tab: the honesty banner, four spend tiles, and the spend-over-time
/// area chart. Every number is an estimate at public list rates, not a bill.
struct AnalyticsLedgerTab: View {
    let ledger: AnalyticsLedger?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if let ledger, !ledger.isEmpty {
                banner(ledger)
                tilesRow(ledger)
                spendOverTime(ledger)
            } else if ledger != nil {
                emptyState
            } else {
                AnalyticsEmptyHint(text: "Spend data hasn't loaded yet.")
                    .padding(.top, 60)
            }
        }
    }

    // MARK: - Banner

    private func banner(_ ledger: AnalyticsLedger) -> some View {
        let excluded = ledger.coverage.excludedByProvider.filter { $0.sessionCount > 0 }
        let excludedLine: String? = excluded.isEmpty ? nil : {
            let parts = excluded.map { entry -> String in
                let name = AgentPalette.displayName(entry.provider)
                return "\(name) (\(entry.sessionCount) session\(entry.sessionCount == 1 ? "" : "s"))"
            }
            return "Excluded from spend (no per-message tokens in the transcript): \(parts.joined(separator: ", "))."
        }()

        return AnalyticsInfoBanner(
            icon: "dollarsign.circle",
            lead: "Estimated public-rate cost, not a bill.",
            text: "Spend is computed from per-message token usage at published list prices and may differ from what you were actually charged (plans, discounts, and free tiers are not modeled).",
            secondary: excludedLine
        )
    }

    // MARK: - Tiles

    private func tilesRow(_ ledger: AnalyticsLedger) -> some View {
        let activeDays = ledger.activeSpendDays
        let perDay = activeDays > 0 ? ledger.totals.estCostUsd / Double(activeDays) : 0
        let coverage = ledger.coverage
        let coverageRate = coverage.totalSessions > 0
            ? Double(coverage.pricedSessions) / Double(coverage.totalSessions)
            : nil

        return HStack(alignment: .top, spacing: 10) {
            AnalyticsTile(
                icon: "dollarsign.circle",
                label: "Estimated spend",
                value: AnalyticsFormat.usd(ledger.totals.estCostUsd),
                sublabel: "public list rates · not a bill",
                accent: true
            )
            AnalyticsTile(
                icon: "arrow.counterclockwise.circle",
                label: "Cache savings",
                value: AnalyticsFormat.usd(ledger.totals.cacheSavingsUsd),
                sublabel: "vs. uncached input pricing"
            )
            AnalyticsTile(
                icon: "calendar",
                label: "Per active day",
                value: AnalyticsFormat.usd(perDay),
                sublabel: "\(activeDays) day\(activeDays == 1 ? "" : "s") with spend"
            )
            AnalyticsTile(
                icon: "checkmark.shield",
                label: "Priced coverage",
                value: AnalyticsFormat.rate(coverageRate),
                sublabel: "\(coverage.pricedSessions)/\(coverage.totalSessions) sessions priced"
            )
        }
    }

    // MARK: - Spend over time

    private struct SpendPoint: Identifiable {
        let date: Date
        let cost: Double
        var id: Date { date }
    }

    /// spendByDay is sparse (only days with data): zero-fill across the window
    /// so the area chart keeps a continuous axis.
    private func spendSeries(_ ledger: AnalyticsLedger) -> [SpendPoint] {
        let byDay = Dictionary(ledger.spendByDay.map { ($0.date, $0.estCostUsd) },
                               uniquingKeysWith: { first, _ in first })
        guard let from = ledger.meta.fromDate, let to = ledger.meta.toDate, from < to else {
            return ledger.spendByDay.compactMap { day in
                day.dayDate.map { SpendPoint(date: $0, cost: day.estCostUsd) }
            }
        }
        var points: [SpendPoint] = []
        var cursor = from
        while cursor < to {
            let key = AnalyticsDay.key(for: cursor)
            points.append(SpendPoint(date: cursor, cost: byDay[key] ?? 0))
            cursor = cursor.addingTimeInterval(86_400)
        }
        return points
    }

    private func spendOverTime(_ ledger: AnalyticsLedger) -> some View {
        let points = spendSeries(ledger)
        let total = ledger.spendByDay.reduce(0) { $0 + $1.estCostUsd }

        return AnalyticsCard(title: "Spend over time", subtitle: "Estimated daily cost (UTC)") {
            if total <= 0 {
                AnalyticsEmptyHint(text: "No priced sessions in this window.")
            } else {
                Chart(points) { point in
                    AreaMark(
                        x: .value("Day", point.date, unit: .day),
                        y: .value("Cost", point.cost)
                    )
                    .foregroundStyle(
                        LinearGradient(colors: [Theme.accent.opacity(0.25), Theme.accent.opacity(0.02)],
                                       startPoint: .top, endPoint: .bottom)
                    )
                    .interpolationMethod(.monotone)
                    LineMark(
                        x: .value("Day", point.date, unit: .day),
                        y: .value("Cost", point.cost)
                    )
                    .foregroundStyle(Theme.accent)
                    .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
                    .interpolationMethod(.monotone)
                }
                .chartXAxis {
                    AxisMarks(values: .automatic(desiredCount: 5)) { _ in
                        AxisValueLabel(format: .dateTime.month(.abbreviated).day())
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                .chartYAxis {
                    AxisMarks(values: .automatic(desiredCount: 4)) { value in
                        AxisGridLine().foregroundStyle(Theme.hairline)
                        AxisValueLabel {
                            if let cost = value.as(Double.self) {
                                Text(AnalyticsFormat.usdCompact(cost))
                                    .font(Theme.body(10))
                                    .foregroundStyle(Theme.inkTertiary)
                            }
                        }
                    }
                }
                .frame(height: 200)
            }
        }
    }

    // MARK: - Empty

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "dollarsign.circle")
                .font(.system(size: 34, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text("No spend in this window")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("Only Claude Code, Codex, and Gemini transcripts carry per-message token usage today. Once you run one of them, its estimated cost will show up here.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 400)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 90)
    }
}
