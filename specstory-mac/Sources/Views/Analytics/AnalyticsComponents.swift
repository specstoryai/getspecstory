import SwiftUI
import SpecStoryKit

// MARK: - Formatting

/// Swift ports of the web app's analyticsFormat/ledgerFormat helpers so the
/// native suite reads identically to the cloud one.
enum AnalyticsFormat {
    /// 0..1 fraction to a whole percent; "-" when unknown.
    static func rate(_ value: Double?) -> String {
        guard let value, value.isFinite else { return "-" }
        return "\(Int((value * 100).rounded()))%"
    }

    /// Ratio delta to a signed percent ("+12%").
    static func deltaPct(_ value: Double?) -> String? {
        guard let value, value.isFinite else { return nil }
        let percent = Int((value * 100).rounded())
        return percent >= 0 ? "+\(percent)%" : "\(percent)%"
    }

    static func deltaAbs(_ value: Double) -> String {
        let rounded = Int(value.rounded())
        return rounded >= 0 ? "+\(rounded.formatted())" : rounded.formatted()
    }

    /// 1500 -> "1.5k", 2000000 -> "2M".
    static func compact(_ value: Int) -> String {
        let magnitude = abs(value)
        switch magnitude {
        case ..<1000:
            return value.formatted()
        case ..<1_000_000:
            let scaled = Double(value) / 1000
            return trimTrailingZero(scaled) + "k"
        default:
            let scaled = Double(value) / 1_000_000
            return trimTrailingZero(scaled) + "M"
        }
    }

    static func compact(_ value: Double) -> String {
        compact(Int(value.rounded()))
    }

    /// "$1,429.43".
    static func usd(_ value: Double) -> String {
        let formatter = NumberFormatter()
        formatter.numberStyle = .currency
        formatter.currencyCode = "USD"
        formatter.locale = Locale(identifier: "en_US")
        return formatter.string(from: NSNumber(value: value)) ?? "$0.00"
    }

    /// "$0.42", "$340", "$1.2k" for axis labels.
    static func usdCompact(_ value: Double) -> String {
        let magnitude = abs(value)
        switch magnitude {
        case ..<1:
            return String(format: "$%.2f", value)
        case ..<1000:
            return "$\(Int(value.rounded()))"
        default:
            return "$" + trimTrailingZero(value / 1000) + "k"
        }
    }

    /// "1h 30m" / "45s".
    static func duration(ms: Double) -> String {
        let seconds = Int((ms / 1000).rounded())
        if seconds < 60 { return "\(seconds)s" }
        let minutes = seconds / 60
        if minutes < 60 { return "\(minutes)m" }
        let hours = minutes / 60
        let rest = minutes % 60
        return rest == 0 ? "\(hours)h" : "\(hours)h \(rest)m"
    }

    /// "Jun 12" from a `YYYY-MM-DD` day key.
    static func dayLabel(_ key: String) -> String {
        guard let date = AnalyticsDay.parse(key) else { return key }
        return dayLabel(date)
    }

    static func dayLabel(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "UTC")
        formatter.dateFormat = "MMM d"
        return formatter.string(from: date)
    }

    private static func trimTrailingZero(_ value: Double) -> String {
        let one = String(format: "%.1f", value)
        return one.hasSuffix(".0") ? String(one.dropLast(2)) : one
    }
}

// MARK: - Agent palette

/// Stable per-agent identity: cloud agent names resolve to Provider badge
/// colors (the app-wide brand identities); unknown names fall back to a fixed
/// theme cycle keyed by a stable hash so the color follows the entity.
enum AgentPalette {
    static func provider(for agentName: String) -> Provider? {
        Provider(providerID: agentName)
    }

    static func displayName(_ agentName: String) -> String {
        if let provider = provider(for: agentName) { return provider.displayName }
        if agentName.isEmpty || agentName == "Unknown" { return "Unknown" }
        return agentName
    }

    static func color(for agentName: String) -> Color {
        if let provider = provider(for: agentName) { return provider.badgeColor }
        let cycle: [Color] = [
            Theme.accent,
            Theme.accent.opacity(0.6),
            Theme.inkSecondary,
            Theme.inkTertiary,
            Theme.accent.opacity(0.35),
            Theme.ink.opacity(0.5),
        ]
        let hash = agentName.unicodeScalars.reduce(0) { ($0 &* 31 &+ Int($1.value)) & 0x7FFF_FFFF }
        return cycle[hash % cycle.count]
    }
}

// MARK: - Grade colors

/// Spec Score letter styling: A/B in the brand accent family, C neutral,
/// D/F in the destructive family, matching the web's gradeColorClass.
enum GradeStyle {
    static func barColor(_ grade: String) -> Color {
        switch grade.uppercased() {
        case "A": return Theme.accent
        case "B": return Theme.accent.opacity(0.55)
        case "C": return Theme.inkTertiary
        case "D": return Theme.live.opacity(0.55)
        default: return Theme.live
        }
    }

    static func chipForeground(_ grade: String) -> Color {
        switch grade.uppercased() {
        case "A", "B": return Theme.accent
        case "C": return Theme.inkSecondary
        default: return Theme.live
        }
    }
}

/// A small "B" letter chip with grade tinting (optionally "B · 64").
struct GradeChip: View {
    let grade: String
    var detail: String? = nil

    var body: some View {
        Text(detail.map { "\(grade) · \($0)" } ?? grade)
            .font(Theme.body(10, weight: .semibold))
            .monospacedDigit()
            .foregroundStyle(GradeStyle.chipForeground(grade))
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(GradeStyle.chipForeground(grade).opacity(0.12))
            .clipShape(Capsule())
            .overlay(Capsule().strokeBorder(GradeStyle.chipForeground(grade).opacity(0.3), lineWidth: 1))
    }
}

// MARK: - Trend badge

/// Up/down/flat delta chip; up reads green, down red, flat gray.
struct TrendBadge: View {
    let direction: String   // "up" | "down" | "flat"
    let text: String

    private var color: Color {
        switch direction {
        case "up": return Theme.synced
        case "down": return Theme.live
        default: return Theme.inkTertiary
        }
    }

    private var symbol: String {
        switch direction {
        case "up": return "arrow.up.right"
        case "down": return "arrow.down.right"
        default: return "arrow.right"
        }
    }

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: symbol)
                .font(.system(size: 8, weight: .bold))
            Text(text)
                .font(Theme.body(10, weight: .semibold))
                .monospacedDigit()
        }
        .foregroundStyle(color)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(color.opacity(0.10))
        .clipShape(Capsule())
    }
}

// MARK: - Stat tile

/// The analytics hero tile: icon + uppercase label, big serif value, quiet
/// sublabel, optional footer row (chips, links). Sibling of StatTile with the
/// extra chrome the cloud cards carry.
struct AnalyticsTile<Footer: View>: View {
    let icon: String
    let label: String
    let value: String
    var sublabel: String? = nil
    var accent = false
    @ViewBuilder var footer: () -> Footer

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 5) {
                Image(systemName: icon)
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(accent ? Theme.accent : Theme.inkTertiary)
                Text(label)
                    .font(Theme.body(10, weight: .medium))
                    .foregroundStyle(Theme.inkTertiary)
                    .textCase(.uppercase)
                    .kerning(0.4)
            }
            Text(value)
                .font(Theme.display(24))
                .foregroundStyle(accent ? Theme.accent : Theme.ink)
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.6)
            if let sublabel {
                Text(sublabel)
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkSecondary)
                    .lineLimit(1)
            }
            footer()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .cardChrome()
    }
}

extension AnalyticsTile where Footer == EmptyView {
    init(icon: String, label: String, value: String, sublabel: String? = nil, accent: Bool = false) {
        self.init(icon: icon, label: label, value: value, sublabel: sublabel, accent: accent) { EmptyView() }
    }
}

// MARK: - Card scaffold

/// Chart/section card: uppercase title, quiet subtitle, optional trailing
/// action, content below.
struct AnalyticsCard<Action: View, Content: View>: View {
    let title: String
    var subtitle: String? = nil
    @ViewBuilder var action: () -> Action
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(Theme.body(12, weight: .semibold))
                        .foregroundStyle(Theme.inkTertiary)
                        .textCase(.uppercase)
                        .kerning(0.4)
                    if let subtitle {
                        Text(subtitle)
                            .font(Theme.body(11))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                }
                Spacer()
                action()
            }
            content()
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }
}

extension AnalyticsCard where Action == EmptyView {
    init(title: String, subtitle: String? = nil, @ViewBuilder content: @escaping () -> Content) {
        self.init(title: title, subtitle: subtitle, action: { EmptyView() }, content: content)
    }
}

// MARK: - Small pieces

/// Quiet inline hint used inside cards when a section has no data.
struct AnalyticsEmptyHint: View {
    let text: String

    var body: some View {
        Text(text)
            .font(Theme.body(11))
            .foregroundStyle(Theme.inkTertiary)
            .frame(maxWidth: .infinity, alignment: .center)
            .padding(.vertical, 24)
    }
}

/// Info banner (the ledger honesty banner, the Spec Score explainer).
struct AnalyticsInfoBanner: View {
    let icon: String
    let lead: String
    let text: String
    var secondary: String? = nil

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: icon)
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(Theme.accent)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 4) {
                (Text(lead).font(Theme.body(11, weight: .semibold)) + Text(" \(text)").font(Theme.body(11)))
                    .foregroundStyle(Theme.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let secondary {
                    Text(secondary)
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.accent.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
                .strokeBorder(Theme.accent.opacity(0.25), lineWidth: 1)
        )
    }
}

/// Legend chip row for agent-stacked charts: color square + display name.
struct AgentLegend: View {
    let agents: [String]

    var body: some View {
        HStack(spacing: 12) {
            ForEach(agents, id: \.self) { agent in
                HStack(spacing: 4) {
                    RoundedRectangle(cornerRadius: 1.5, style: .continuous)
                        .fill(AgentPalette.color(for: agent))
                        .frame(width: 8, height: 8)
                    Text(AgentPalette.displayName(agent))
                        .font(Theme.body(10))
                        .foregroundStyle(Theme.inkSecondary)
                }
            }
        }
    }
}

// MARK: - Tab bar

/// Underline-style tab strip matching the web app's analytics layout.
struct AnalyticsTabBar: View {
    @Binding var selection: AnalyticsTab

    var body: some View {
        HStack(spacing: 18) {
            ForEach(AnalyticsTab.allCases) { tab in
                Button {
                    selection = tab
                } label: {
                    VStack(spacing: 6) {
                        Text(tab.title)
                            .font(Theme.body(12, weight: selection == tab ? .semibold : .regular))
                            .foregroundStyle(selection == tab ? Theme.ink : Theme.inkSecondary)
                        Rectangle()
                            .fill(selection == tab ? Theme.accent : .clear)
                            .frame(height: 2)
                    }
                    .fixedSize(horizontal: true, vertical: false)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.tactile)
                .accessibilityAddTraits(selection == tab ? [.isSelected] : [])
            }
            Spacer()
        }
        .overlay(alignment: .bottom) {
            Rectangle().fill(Theme.hairline).frame(height: 1)
        }
    }
}

// MARK: - Stacked-by-agent day series helpers

/// Flattened mark for Swift Charts stacked bars: one (day, agent, count) row.
struct AgentDayMark: Identifiable {
    let day: Date
    let agent: String
    let count: Int
    var id: String { "\(day.timeIntervalSince1970)-\(agent)" }
}

enum AgentDaySeries {
    /// Agents ordered by window volume (descending), the shared stack and
    /// legend order.
    static func orderedAgents(_ days: [AnalyticsDayAgentCounts]) -> [String] {
        var totals: [String: Int] = [:]
        for day in days {
            for (agent, count) in day.perAgent {
                totals[agent, default: 0] += count
            }
        }
        return totals.sorted { lhs, rhs in
            if lhs.value != rhs.value { return lhs.value > rhs.value }
            return lhs.key < rhs.key
        }.map(\.key)
    }

    /// Flattens the zero-filled day rows into stacked marks. Agents stack in
    /// shared volume order so every chart on the page agrees.
    static func marks(_ days: [AnalyticsDayAgentCounts], agents: [String]) -> [AgentDayMark] {
        var marks: [AgentDayMark] = []
        for day in days {
            guard let date = day.dayDate else { continue }
            for agent in agents {
                let count = day.perAgent[agent] ?? 0
                if count > 0 {
                    marks.append(AgentDayMark(day: date, agent: agent, count: count))
                }
            }
        }
        return marks
    }
}
