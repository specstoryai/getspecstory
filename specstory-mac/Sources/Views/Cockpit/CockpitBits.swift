import AppKit
import SwiftUI

/// Cockpit-only colors that Theme does not carry: diff tints and the amber
/// used for the waiting-for-permission state.
enum CockpitPalette {
    static var added: Color { Theme.synced }

    static let removed = Theme.dynamicColor(
        light: NSColor(red: 0.76, green: 0.26, blue: 0.24, alpha: 1),
        dark: NSColor(red: 0.93, green: 0.51, blue: 0.48, alpha: 1))

    static let amber = Theme.dynamicColor(
        light: NSColor(red: 0.78, green: 0.52, blue: 0.09, alpha: 1),
        dark: NSColor(red: 0.95, green: 0.72, blue: 0.34, alpha: 1))
}

/// Formatting helpers for the cockpit readouts.
enum CockpitFormat {
    /// 842 -> "842", 12_400 -> "12.4k", 111_000 -> "111k", 2_400_000 -> "2.4M".
    static func compactTokens(_ count: Int) -> String {
        if count < 1000 { return "\(count)" }
        if count < 1_000_000 {
            let thousands = Double(count) / 1000
            return thousands >= 99.95
                ? "\(Int(thousands.rounded()))k"
                : String(format: "%.1fk", thousands)
        }
        let millions = Double(count) / 1_000_000
        return millions >= 99.95
            ? "\(Int(millions.rounded()))M"
            : String(format: "%.1fM", millions)
    }

    /// "in 12.4k · out 3.2k · cache 111k"; nil when no counter is known.
    static func tokenUsage(input: Int?, output: Int?, cacheRead: Int?) -> String? {
        var parts: [String] = []
        if let input { parts.append("in \(compactTokens(input))") }
        if let output { parts.append("out \(compactTokens(output))") }
        if let cacheRead { parts.append("cache \(compactTokens(cacheRead))") }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    /// Home-relative display path for the header strip.
    static func abbreviatePath(_ path: String) -> String {
        let home = NSHomeDirectory()
        if path == home { return "~" }
        if path.hasPrefix(home + "/") {
            return "~" + path.dropFirst(home.count)
        }
        return path
    }

    static func fileName(_ path: String) -> String {
        (path as NSString).lastPathComponent
    }
}

/// One clickable file chip in the touched-files row.
struct CockpitFileChip: View {
    let label: String
    var icon: String = "doc"
    var selected = false
    var help: String?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.system(size: 8.5))
                Text(label)
                    .font(Theme.mono(10))
                    .lineLimit(1)
            }
            .foregroundStyle(selected ? Theme.accent : Theme.inkSecondary)
            .padding(.horizontal, 8)
            .padding(.vertical, 3.5)
            .background(
                selected ? Theme.accent.opacity(0.10) : Theme.sidebarSelection,
                in: Capsule()
            )
            .overlay(
                Capsule().strokeBorder(
                    selected ? Theme.accent.opacity(0.45) : Color.clear, lineWidth: 1)
            )
            .contentShape(Capsule())
        }
        .buttonStyle(.tactile)
        .help(help ?? label)
    }
}

/// Quiet capsule action for the cockpit footer.
struct CockpitActionButton: View {
    let title: String
    let icon: String
    var tint: Color = Theme.inkSecondary
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Image(systemName: icon)
                    .font(.system(size: 10, weight: .medium))
                Text(title)
                    .font(Theme.body(11, weight: .medium))
            }
            .foregroundStyle(tint)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(Theme.sidebarSelection, in: Capsule())
            .contentShape(Capsule())
        }
        .buttonStyle(.tactile)
        .help(title)
    }
}

/// The breathing activity dot for the cockpit status line.
struct CockpitPulseDot: View {
    var color: Color = Theme.live

    @State private var pulsing = false

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: 6, height: 6)
            .opacity(pulsing ? 0.35 : 1)
            .animation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true), value: pulsing)
            .onAppear { pulsing = true }
    }
}
