import AppKit
import SwiftUI
import SpecStoryKit

/// Design tokens for the Granola-style shell: warm paper surfaces, hairline
/// borders, serif display headers, quiet grays. Light-first with dark support.
enum Theme {
    // MARK: Surfaces

    static let paper = dynamicColor(light: NSColor(red: 0.984, green: 0.980, blue: 0.968, alpha: 1),
                                    dark: NSColor(red: 0.113, green: 0.111, blue: 0.105, alpha: 1))
    static let card = dynamicColor(light: .white,
                                   dark: NSColor(red: 0.157, green: 0.155, blue: 0.148, alpha: 1))
    static let cardHover = dynamicColor(light: NSColor(red: 0.975, green: 0.970, blue: 0.958, alpha: 1),
                                        dark: NSColor(red: 0.190, green: 0.188, blue: 0.180, alpha: 1))
    static let hairline = dynamicColor(light: NSColor.black.withAlphaComponent(0.08),
                                       dark: NSColor.white.withAlphaComponent(0.10))
    static let sidebarSelection = dynamicColor(light: NSColor.black.withAlphaComponent(0.06),
                                               dark: NSColor.white.withAlphaComponent(0.09))

    // MARK: Text

    static let ink = dynamicColor(light: NSColor(red: 0.13, green: 0.12, blue: 0.11, alpha: 1),
                                  dark: NSColor(red: 0.93, green: 0.92, blue: 0.90, alpha: 1))
    static let inkSecondary = dynamicColor(light: NSColor(red: 0.42, green: 0.41, blue: 0.39, alpha: 1),
                                           dark: NSColor(red: 0.66, green: 0.65, blue: 0.63, alpha: 1))
    static let inkTertiary = dynamicColor(light: NSColor(red: 0.62, green: 0.61, blue: 0.58, alpha: 1),
                                          dark: NSColor(red: 0.48, green: 0.47, blue: 0.45, alpha: 1))

    // MARK: Accents

    static let accent = dynamicColor(light: NSColor(red: 0.20, green: 0.55, blue: 0.72, alpha: 1),
                                     dark: NSColor(red: 0.38, green: 0.70, blue: 0.85, alpha: 1))
    static let live = dynamicColor(light: NSColor(red: 0.85, green: 0.33, blue: 0.25, alpha: 1),
                                   dark: NSColor(red: 0.95, green: 0.45, blue: 0.36, alpha: 1))
    static let synced = dynamicColor(light: NSColor(red: 0.23, green: 0.60, blue: 0.36, alpha: 1),
                                     dark: NSColor(red: 0.38, green: 0.75, blue: 0.50, alpha: 1))

    // MARK: Type

    /// Serif display face for section headers (the Granola "Coming up" look).
    static func display(_ size: CGFloat, weight: Font.Weight = .semibold) -> Font {
        .system(size: size, weight: weight, design: .serif)
    }

    static func body(_ size: CGFloat, weight: Font.Weight = .regular) -> Font {
        .system(size: size, weight: weight)
    }

    static func mono(_ size: CGFloat) -> Font {
        .system(size: size, design: .monospaced)
    }

    // MARK: Metrics

    static let cardRadius: CGFloat = 10
    static let feedWidth: CGFloat = 720

    private static func dynamicColor(light: NSColor, dark: NSColor) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? dark : light
        })
    }
}

/// Visual identity per agent for badges and dots.
extension Provider {
    var badgeColor: Color {
        switch self {
        case .claude: return Color(red: 0.85, green: 0.47, blue: 0.34)
        case .codex: return Color(red: 0.06, green: 0.64, blue: 0.50)
        case .cursor, .cursoride: return Color(red: 0.35, green: 0.34, blue: 0.33)
        case .gemini: return Color(red: 0.26, green: 0.52, blue: 0.96)
        case .antigravity: return Color(red: 0.55, green: 0.36, blue: 0.86)
        case .deepseek: return Color(red: 0.30, green: 0.42, blue: 1.00)
        case .droid: return Color(red: 0.55, green: 0.53, blue: 0.50)
        }
    }
}

/// Card chrome shared by feed rows, source pills, and popover rows.
struct CardBackground: ViewModifier {
    var hovering = false

    func body(content: Content) -> some View {
        content
            .background(hovering ? Theme.cardHover : Theme.card)
            .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
                    .strokeBorder(Theme.hairline, lineWidth: 1)
            )
            .shadow(color: .black.opacity(hovering ? 0.07 : 0.04), radius: hovering ? 6 : 3, y: 1)
    }
}

extension View {
    func cardChrome(hovering: Bool = false) -> some View {
        modifier(CardBackground(hovering: hovering))
    }
}
