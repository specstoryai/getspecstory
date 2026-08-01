import AppKit
import SwiftUI
import SpecStoryKit

/// Shared visual vocabulary for the Skills surface: the amber review tint,
/// state, phase, and verdict chip colors, and compact time formatting that
/// mirrors the web activity panel.
enum SkillsStyle {
    /// Amber for review states and judging phases; Theme has no amber token.
    static let amber = Theme.dynamicColor(light: NSColor(red: 0.72, green: 0.50, blue: 0.08, alpha: 1),
                                          dark: NSColor(red: 0.92, green: 0.72, blue: 0.34, alpha: 1))

    // MARK: Skill states

    static func stateLabel(_ state: String?) -> String? {
        switch state {
        case "review": return "Review"
        case "ready": return "Ready"
        case "installed": return "Installed"
        default: return nil
        }
    }

    static func stateColor(_ state: String?) -> Color {
        switch state {
        case "review": return amber
        case "installed": return Theme.synced
        default: return Theme.accent
        }
    }

    static func stateGlyph(_ state: String?) -> String {
        switch state {
        case "review": return "exclamationmark.triangle"
        case "installed": return "checkmark"
        default: return "sparkles"
        }
    }

    // MARK: Run phases

    static func phaseColor(_ phase: SkillRunPhase) -> Color {
        switch phase {
        case .done: return Theme.synced
        case .failed: return Theme.live
        case .running, .sharding: return Theme.accent
        case .judging, .reducing: return amber
        default: return Theme.inkTertiary
        }
    }

    // MARK: Verdicts

    static func verdictColor(_ verdict: String?) -> Color {
        switch verdict {
        case "confirmed": return Theme.synced
        case "needs-edits": return amber
        case "refuted": return Theme.live
        default: return Theme.inkTertiary
        }
    }

    // MARK: Time

    /// "just now" / "Nm ago" / "Nh ago" / a full date, like the web fmtWhen.
    static func relativeLabel(for date: Date?, now: Date = Date()) -> String? {
        guard let date else { return nil }
        let seconds = now.timeIntervalSince(date)
        if seconds < 60 { return "just now" }
        if seconds < 3_600 { return "\(Int(seconds / 60))m ago" }
        if seconds < 86_400 { return "\(Int(seconds / 3_600))h ago" }
        return date.formatted(date: .abbreviated, time: .shortened)
    }

    /// Compact duration: "42s", "3m 08s", "1h 05m".
    static func durationLabel(_ interval: TimeInterval?) -> String? {
        guard let interval else { return nil }
        let total = Int(interval.rounded())
        if total < 60 { return "\(total)s" }
        let minutes = total / 60
        if minutes < 60 { return "\(minutes)m \(String(format: "%02d", total % 60))s" }
        return "\(minutes / 60)h \(String(format: "%02d", minutes % 60))m"
    }
}

/// The small capsule chip used for skill states, run phases, and verdicts.
struct SkillsChip: View {
    let label: String
    let color: Color

    var body: some View {
        Text(label)
            .font(Theme.body(10, weight: .medium))
            .foregroundStyle(color)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(color.opacity(0.12), in: Capsule())
    }
}

/// Uppercase micro-heading used inside cards ("Skills produced", "Shards").
struct SkillsSectionCaption: View {
    let text: String

    var body: some View {
        Text(text.uppercased())
            .font(Theme.body(9.5, weight: .semibold))
            .kerning(0.8)
            .foregroundStyle(Theme.inkTertiary)
    }
}
