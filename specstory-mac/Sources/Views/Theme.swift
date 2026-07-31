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

    static func dynamicColor(light: NSColor, dark: NSColor) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? dark : light
        })
    }
}

/// Visual identity per agent for badges and dots. Every color adapts to dark
/// mode; near-black brand tones (Cursor) especially need a light variant.
extension Provider {
    var badgeColor: Color {
        switch self {
        case .claude:
            return Theme.dynamicColor(light: NSColor(red: 0.85, green: 0.47, blue: 0.34, alpha: 1),
                                      dark: NSColor(red: 0.93, green: 0.58, blue: 0.45, alpha: 1))
        case .codex:
            return Theme.dynamicColor(light: NSColor(red: 0.06, green: 0.64, blue: 0.50, alpha: 1),
                                      dark: NSColor(red: 0.25, green: 0.78, blue: 0.63, alpha: 1))
        case .cursor, .cursoride:
            return Theme.dynamicColor(light: NSColor(red: 0.35, green: 0.34, blue: 0.33, alpha: 1),
                                      dark: NSColor(red: 0.78, green: 0.77, blue: 0.75, alpha: 1))
        case .gemini:
            return Theme.dynamicColor(light: NSColor(red: 0.26, green: 0.52, blue: 0.96, alpha: 1),
                                      dark: NSColor(red: 0.45, green: 0.65, blue: 1.00, alpha: 1))
        case .antigravity:
            return Theme.dynamicColor(light: NSColor(red: 0.55, green: 0.36, blue: 0.86, alpha: 1),
                                      dark: NSColor(red: 0.70, green: 0.55, blue: 0.95, alpha: 1))
        case .deepseek:
            return Theme.dynamicColor(light: NSColor(red: 0.30, green: 0.42, blue: 1.00, alpha: 1),
                                      dark: NSColor(red: 0.50, green: 0.60, blue: 1.00, alpha: 1))
        case .droid:
            return Theme.dynamicColor(light: NSColor(red: 0.55, green: 0.53, blue: 0.50, alpha: 1),
                                      dark: NSColor(red: 0.72, green: 0.70, blue: 0.67, alpha: 1))
        case .copilotide:
            return Theme.dynamicColor(light: NSColor(red: 0.14, green: 0.16, blue: 0.18, alpha: 1),
                                      dark: NSColor(red: 0.80, green: 0.82, blue: 0.85, alpha: 1))
        }
    }

    /// Brand icon assets sourced from the SpecStory Cloud web app
    /// (sync-cloud/public/assets). Cursor CLI and Cursor IDE share a mark.
    var iconAssetName: String {
        switch self {
        case .cursoride: return "provider-cursor"
        case .copilotide: return "provider-copilot"
        default: return "provider-\(rawValue)"
        }
    }

    /// currentColor SVGs became template images: tint with the brand color.
    /// Cursor, Droid, and Antigravity ship their own colors.
    var iconIsTemplate: Bool {
        switch self {
        case .claude, .codex, .gemini, .deepseek, .copilotide: return true
        case .cursor, .cursoride, .droid, .antigravity: return false
        }
    }
}

/// The rounded provider mark used on cards, rows, and detail headers.
struct ProviderIcon: View {
    let provider: Provider?
    var fallbackName: String = "?"
    var size: CGFloat = 30

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.23, style: .continuous)
                .fill((provider?.badgeColor ?? Theme.inkSecondary).opacity(0.12))
            if let provider {
                Image(provider.iconAssetName)
                    .resizable()
                    .renderingMode(provider.iconIsTemplate ? .template : .original)
                    .scaledToFit()
                    .foregroundStyle(provider.badgeColor)
                    .frame(width: size * 0.60, height: size * 0.60)
            } else {
                Text(String(fallbackName.prefix(1)).uppercased())
                    .font(Theme.body(size * 0.43, weight: .semibold))
                    .foregroundStyle(Theme.inkSecondary)
            }
        }
        .frame(width: size, height: size)
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
