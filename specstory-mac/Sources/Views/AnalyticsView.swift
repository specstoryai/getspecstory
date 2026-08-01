import Charts
import SwiftUI
import SpecStoryKit

/// The native SpecStory Cloud analytics suite: Overview | Activity | Messages
/// | Ledger with a 7/30/90 day range picker, wrapped in the Pro gate. When the
/// gate is not enabled the same page renders from sample fixtures so the
/// blurred preview looks real (the Granola pattern). When cloud data is not
/// available (signed out, network trouble) the local SQLite snapshot carries
/// the panel.
struct AnalyticsView: View {
    @ObservedObject var analytics: AnalyticsModel
    @ObservedObject var pro: ProModel
    let model: AppModel

    private var gated: Bool { pro.analyticsGate != .enabled }

    var body: some View {
        ProGateView(
            gate: pro.analyticsGate,
            featureBlurb: "Analytics for your coding sessions are part of SpecStory Pro.",
            planName: "Pro",
            planPrice: "$25/mo",
            onUpgrade: { pro.openCheckout() },
            onManagePlan: { pro.openPortal() }
        ) {
            AnalyticsPanel(analytics: analytics, gated: gated)
        }
        .background(Theme.paper)
        .task {
            analytics.configure(auth: model.auth)
            if gated {
                analytics.refreshLocal()
            } else {
                analytics.refresh()
            }
        }
        .onChange(of: pro.analyticsGate) { _, gate in
            if gate == .enabled {
                analytics.refresh()
            }
        }
    }
}

/// The gate's content: full page whether it draws real cloud data, sample
/// fixtures (gated underlay), or the local fallback.
private struct AnalyticsPanel: View {
    @ObservedObject var analytics: AnalyticsModel
    let gated: Bool

    // Data routing: gated shows the sample page; enabled shows cloud data.
    private var overview: AnalyticsOverview? { gated ? AnalyticsModel.sampleOverview : analytics.overview }
    private var ledger: AnalyticsLedger? { gated ? AnalyticsModel.sampleLedger : analytics.ledger }
    private var specScore: AnalyticsSpecScore? { gated ? AnalyticsModel.sampleSpecScore : analytics.specScore }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                AnalyticsTabBar(selection: $analytics.tab)

                if let overview {
                    tabContent(overview)
                } else if analytics.cloudLoading {
                    loadingState
                } else {
                    fallbackContent
                }
            }
            .padding(28)
            .frame(maxWidth: Theme.feedWidth, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .background(Theme.paper)
    }

    // MARK: - Header

    private var header: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 10) {
                    Text("Analytics")
                        .font(Theme.display(24))
                        .foregroundStyle(Theme.ink)
                    if analytics.cloudLoading && analytics.overview != nil {
                        ProgressView().controlSize(.small)
                    }
                }
                Text("Cross-agent overview of your coding sessions")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
            }
            Spacer()
            rangePicker
        }
        .padding(.bottom, 2)
    }

    private var rangePicker: some View {
        Picker("Range", selection: Binding(
            get: { analytics.range },
            set: { analytics.refresh(range: $0) }
        )) {
            ForEach(AnalyticsRange.allCases) { range in
                Text(range.displayName).tag(range)
            }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .frame(width: 240)
    }

    // MARK: - Tab routing

    @ViewBuilder
    private func tabContent(_ overview: AnalyticsOverview) -> some View {
        if let error = analytics.cloudError, !gated {
            errorBanner(error)
        }
        switch analytics.tab {
        case .overview:
            AnalyticsOverviewTab(
                overview: overview,
                ledger: ledger,
                specScore: specScore,
                openLedger: { analytics.tab = .ledger },
                openActivity: { analytics.tab = .activity },
                openMessages: { analytics.tab = .messages }
            )
        case .activity:
            AnalyticsActivityTab(overview: overview)
        case .messages:
            AnalyticsMessagesTab(overview: overview, specScore: specScore)
        case .ledger:
            AnalyticsLedgerTab(ledger: ledger)
        }
    }

    // MARK: - States

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

    private func errorBanner(_ message: String) -> some View {
        HStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(Theme.live)
            Text(message)
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
            Spacer()
            Button("Retry") {
                analytics.refresh(force: true)
            }
            .buttonStyle(.plain)
            .font(Theme.body(11, weight: .semibold))
            .foregroundStyle(Theme.accent)
        }
        .padding(12)
        .background(Theme.live.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
                .strokeBorder(Theme.live.opacity(0.25), lineWidth: 1)
        )
    }

    // MARK: - Local fallback (cloud data unavailable)

    @ViewBuilder
    private var fallbackContent: some View {
        if let error = analytics.cloudError {
            errorBanner(error)
        }
        if analytics.isEmpty {
            if analytics.localLoading {
                loadingState
            } else {
                localEmptyState
            }
        } else {
            AnalyticsLocalFallback(snapshot: analytics.snapshot)
        }
    }

    private var localEmptyState: some View {
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
}

/// The local-index dashboard shown while cloud analytics are unavailable:
/// headline tiles, sessions per day, per-agent split, and the hour histogram,
/// all from ~/.specstory/sessions.db.
private struct AnalyticsLocalFallback: View {
    let snapshot: AnalyticsSnapshot

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 8) {
                Image(systemName: "internaldrive")
                    .font(.system(size: 10, weight: .medium))
                Text("Showing local data from this Mac. Sign in and sync to see the full cloud suite.")
                    .font(Theme.body(11))
            }
            .foregroundStyle(Theme.inkTertiary)

            HStack(spacing: 10) {
                StatTile(label: "Sessions", value: snapshot.sessions.formatted())
                StatTile(label: "Projects", value: snapshot.projects.formatted())
                StatTile(label: "Prompts", value: snapshot.userTurns.formatted())
                StatTile(label: "Day streak", value: snapshot.streak.formatted())
            }

            AnalyticsCard(title: "Sessions per day", subtitle: "Local index, last 30 days") {
                Chart(snapshot.perDay) { day in
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
                .frame(height: 160)
            }

            AnalyticsCard(title: "By agent") {
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(snapshot.providers) { stat in
                        providerRow(stat)
                    }
                    if snapshot.providers.isEmpty {
                        AnalyticsEmptyHint(text: "No agent activity indexed yet.")
                    }
                }
            }

            hourOfDayCard
        }
    }

    private func providerRow(_ stat: AnalyticsProviderStat) -> some View {
        let provider = Provider(providerID: stat.provider)
        let color = provider?.badgeColor ?? Theme.inkSecondary
        let maxSessions = snapshot.providers.map(\.sessions).max() ?? 1
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

            Text("\(stat.sessions.formatted()) session\(stat.sessions == 1 ? "" : "s") · \(stat.userTurns.formatted()) prompt\(stat.userTurns == 1 ? "" : "s")")
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
                .monospacedDigit()
                .lineLimit(1)
                .fixedSize()
        }
    }

    private struct HourBucket: Identifiable {
        let hour: Int
        let count: Int
        var id: Int { hour }
    }

    private var hourOfDayCard: some View {
        let buckets = snapshot.hourHistogram.enumerated().map { HourBucket(hour: $0.offset, count: $0.element) }
        let peakHour = snapshot.hourHistogram.max().flatMap { max in
            max > 0 ? snapshot.hourHistogram.firstIndex(of: max) : nil
        }

        return AnalyticsCard(
            title: "Hour of day",
            subtitle: peakHour.map { "Busiest around \(hourLabel($0))" }
        ) {
            Chart(buckets) { bucket in
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
                            Text(hourLabel(hour))
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

    private func hourLabel(_ hour: Int) -> String {
        let twelve = hour % 12 == 0 ? 12 : hour % 12
        return "\(twelve)\(hour < 12 ? "am" : "pm")"
    }
}
