import SwiftUI

/// The cockpit diff area: monospaced unified diff with simple line-prefix
/// tinting (+ green, - red, @@ dim), scrollable up to ~320pt, with a close
/// button. Large diffs are capped so the feed never chokes on a huge change.
struct CockpitDiffView: View {
    let title: String
    let text: String?
    let loading: Bool
    let onClose: () -> Void

    private static let maxLines = 500

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Rectangle()
                .fill(Theme.hairline)
                .frame(height: 1)
            content
        }
        .background(Theme.paper, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .strokeBorder(Theme.hairline, lineWidth: 1)
        )
    }

    private var header: some View {
        HStack(spacing: 6) {
            Image(systemName: "plus.forwardslash.minus")
                .font(.system(size: 9))
                .foregroundStyle(Theme.inkTertiary)
            Text(title)
                .font(Theme.mono(10))
                .foregroundStyle(Theme.inkSecondary)
                .lineLimit(1)
                .truncationMode(.head)
            Spacer()
            Button(action: onClose) {
                Image(systemName: "xmark")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(Theme.inkTertiary)
                    .frame(width: 18, height: 18)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.tactile)
            .help("Close diff")
            .accessibilityLabel("Close diff")
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
    }

    @ViewBuilder private var content: some View {
        if loading {
            HStack(spacing: 8) {
                ProgressView()
                    .controlSize(.small)
                Text("Reading diff")
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkTertiary)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 24)
        } else if let text {
            let lines = Array(text.split(separator: "\n", omittingEmptySubsequences: false).prefix(Self.maxLines))
            let truncated = text.split(separator: "\n", omittingEmptySubsequences: false).count > Self.maxLines
            ScrollView([.vertical, .horizontal]) {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                        Text(line.isEmpty ? " " : String(line))
                            .font(Theme.mono(10.5))
                            .foregroundStyle(color(for: line))
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    if truncated {
                        Text("Diff truncated at \(Self.maxLines) lines")
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                            .padding(.top, 6)
                    }
                }
                .padding(10)
                .textSelection(.enabled)
            }
            .frame(maxHeight: 320)
        }
    }

    private func color(for line: Substring) -> Color {
        if line.hasPrefix("+++") || line.hasPrefix("---") { return Theme.inkSecondary }
        if line.hasPrefix("+") { return CockpitPalette.added }
        if line.hasPrefix("-") { return CockpitPalette.removed }
        if line.hasPrefix("@@") { return Theme.inkTertiary }
        if line.hasPrefix("diff ") || line.hasPrefix("index ") { return Theme.inkTertiary }
        return Theme.ink
    }
}
