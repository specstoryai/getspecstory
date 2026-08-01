import AppKit
import SwiftUI
import SpecStoryKit

/// Shared formatting for the transcript's role-header timestamps
/// ("2026-06-30 15:24:48Z" or the local "-0700" variant).
enum TranscriptFormat {
    private static let utcParser: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "UTC")
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ss'Z'"
        return formatter
    }()

    private static let offsetParser: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ssZZZ"
        return formatter
    }()

    private static let timeOnly: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateStyle = .none
        formatter.timeStyle = .short
        return formatter
    }()

    static func date(from raw: String?) -> Date? {
        guard let raw else { return nil }
        return utcParser.date(from: raw) ?? offsetParser.date(from: raw)
    }

    static func timeLabel(_ raw: String?) -> String? {
        guard let raw else { return nil }
        guard let date = date(from: raw) else { return raw }
        return timeOnly.string(from: date)
    }
}

/// Icon and tint per tool type, mirroring the cloud viewer's pill styling.
enum ToolStyle {
    static func symbol(for type: String) -> String {
        switch type.lowercased() {
        case "read": return "eye"
        case "search", "grep": return "magnifyingglass"
        case "write": return "pencil"
        case "bash", "shell": return "terminal"
        case "mcp": return "hammer"
        case "task": return "checkmark.circle"
        case "unknown": return "questionmark.circle"
        default: return "wrench.adjustable"
        }
    }

    static func tint(for type: String) -> Color {
        switch type.lowercased() {
        case "read": return .blue
        case "search", "grep": return .purple
        case "write": return .green
        case "bash", "shell": return .orange
        case "mcp": return .pink
        case "task": return .green
        case "unknown": return Theme.inkTertiary
        default: return Theme.inkSecondary
        }
    }
}

/// Renders prose markdown as paragraph blocks (headings get the serif display
/// face). Prose segments never contain code fences; the parser extracts those.
struct ProseBlocksView: View {
    let text: String
    var fontSize: CGFloat = 12.5

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in
                ProseBlockText(block: block, fontSize: fontSize)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var blocks: [String] {
        var result = [String]()
        var current = [String]()
        for line in text.components(separatedBy: "\n") {
            if line.trimmingCharacters(in: .whitespaces).isEmpty {
                if !current.isEmpty {
                    result.append(current.joined(separator: "\n"))
                    current = []
                }
            } else {
                current.append(line)
            }
        }
        if !current.isEmpty {
            result.append(current.joined(separator: "\n"))
        }
        return result
    }
}

private struct ProseBlockText: View {
    let block: String
    let fontSize: CGFloat

    var body: some View {
        if block.hasPrefix("#") {
            Text(block.drop(while: { $0 == "#" || $0 == " " }))
                .font(Theme.display(headingSize))
                .foregroundStyle(Theme.ink)
                .textSelection(.enabled)
                .padding(.top, 4)
        } else {
            Text(rendered)
                .font(Theme.body(fontSize))
                .foregroundStyle(Theme.ink)
                .textSelection(.enabled)
                .lineSpacing(3)
        }
    }

    private var headingSize: CGFloat {
        let level = block.prefix(while: { $0 == "#" }).count
        switch level {
        case 1: return 17
        case 2: return 15
        default: return 13.5
        }
    }

    private var rendered: AttributedString {
        (try? AttributedString(
            markdown: block,
            options: AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        )) ?? AttributedString(block)
    }
}

/// The muted "who is speaking" line for agent turns; consecutive same-model
/// headers were already deduped by the parser.
struct RoleHeaderLine: View {
    let role: String
    let model: String?
    let timestamp: String?
    let sidechain: Bool

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: sidechain ? "arrow.triangle.branch" : "sparkle")
                .font(.system(size: 9, weight: .medium))
            Text(model ?? role)
                .font(Theme.body(10.5, weight: .medium))
            if sidechain {
                Text("subagent")
                    .font(Theme.body(9, weight: .semibold))
                    .padding(.horizontal, 5)
                    .padding(.vertical, 1)
                    .background(Capsule().fill(Theme.sidebarSelection))
            }
            if let time = TranscriptFormat.timeLabel(timestamp) {
                Text(time)
                    .font(Theme.body(10))
            }
        }
        .foregroundStyle(Theme.inkTertiary)
        .padding(.top, 4)
    }
}

/// Thinking: one muted inline line when short, otherwise a collapsed
/// disclosure titled "Thinking".
struct ThinkingSegmentView: View {
    let text: String
    @State private var expanded = false

    private var isInlineShort: Bool {
        !text.contains("\n") && text.count <= 60
    }

    var body: some View {
        if isInlineShort {
            Text("Thinking: \(text)")
                .font(Theme.body(11.5))
                .italic()
                .foregroundStyle(Theme.inkTertiary)
                .textSelection(.enabled)
        } else {
            VStack(alignment: .leading, spacing: 6) {
                Button {
                    withAnimation(.easeOut(duration: 0.15)) { expanded.toggle() }
                } label: {
                    HStack(spacing: 5) {
                        Image(systemName: expanded ? "chevron.down" : "chevron.right")
                            .font(.system(size: 8, weight: .semibold))
                        Text("Thinking")
                            .font(Theme.body(11, weight: .medium))
                    }
                    .foregroundStyle(Theme.inkTertiary)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.tactile)

                if expanded {
                    scrollableIfTall {
                        Text(text)
                            .font(Theme.body(11.5))
                            .foregroundStyle(Theme.inkSecondary)
                            .lineSpacing(3)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(10)
                    }
                    .background(Theme.sidebarSelection, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                }
            }
        }
    }

    @ViewBuilder private func scrollableIfTall(@ViewBuilder content: () -> some View) -> some View {
        if text.components(separatedBy: "\n").count > 24 {
            ScrollView { content() }
                .frame(height: 320)
        } else {
            content()
        }
    }
}

/// Collapsed tool pill with type icon and summary; expands to the formatted
/// body. Write tools default expanded (cloud parity).
struct ToolPillView: View {
    let type: String
    let name: String
    let summary: String
    let detail: String

    @State private var expanded: Bool

    init(type: String, name: String, summary: String, detail: String) {
        self.type = type
        self.name = name
        self.summary = summary
        self.detail = detail
        _expanded = State(initialValue: type.lowercased() == "write" && !detail.isEmpty)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                withAnimation(.easeOut(duration: 0.15)) { expanded.toggle() }
            } label: {
                HStack(spacing: 7) {
                    Image(systemName: ToolStyle.symbol(for: type))
                        .font(.system(size: 10, weight: .medium))
                        .foregroundStyle(ToolStyle.tint(for: type))
                        .frame(width: 14)
                    Text(displayText)
                        .font(Theme.body(11, weight: .medium))
                        .foregroundStyle(Theme.inkSecondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                    Spacer(minLength: 8)
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 8, weight: .semibold))
                        .foregroundStyle(Theme.inkTertiary)
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .contentShape(Rectangle())
            }
            .buttonStyle(.tactile)
            .background(Theme.card)

            if expanded {
                Divider().overlay(Theme.hairline)
                ToolBodyView(detail: detail)
                    .padding(10)
                    .background(Theme.card)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .strokeBorder(Theme.hairline, lineWidth: 1)
        )
        .help(name.isEmpty ? type : name)
    }

    private var displayText: String {
        if summary.isEmpty {
            return name.isEmpty ? type : name
        }
        return summary
    }
}

/// A tool body: prose and fenced code split via the Kit's fence rules, capped
/// at a scrollable height for very long output.
private struct ToolBodyView: View {
    private let segments: [SessionTranscript.Segment]
    private let tall: Bool

    init(detail: String) {
        segments = SessionTranscript.fenceSegments(in: detail)
        tall = detail.reduce(into: 0) { count, character in
            if character == "\n" { count += 1 }
        } > 22
    }

    var body: some View {
        if segments.isEmpty {
            Text("No captured output")
                .font(Theme.body(11))
                .italic()
                .foregroundStyle(Theme.inkTertiary)
        } else if tall {
            ScrollView {
                content
            }
            .frame(height: 360)
        } else {
            content
        }
    }

    private var content: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(segments.enumerated()), id: \.offset) { _, segment in
                switch segment {
                case .prose(let text):
                    ProseBlocksView(text: text, fontSize: 11.5)
                case .code(let language, let filename, let content):
                    CodeBlockView(language: language, filename: filename, content: content)
                default:
                    EmptyView()
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Fenced code with a header bar (filename or language + line count), a Show
/// all toggle past 10 lines, and diff-aware line tinting.
struct CodeBlockView: View {
    let language: String?
    let filename: String?
    let content: String

    @State private var showAll = false

    private static let collapsedLineCount = 10

    private var lines: [String] { content.components(separatedBy: "\n") }

    private var isDiff: Bool {
        if language == "diff" || language == "patch" { return true }
        let all = lines
        if all.contains(where: { $0.hasPrefix("@@") }) { return true }
        let changed = all.filter { $0.hasPrefix("+") || $0.hasPrefix("-") }.count
        let nonEmpty = all.filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }.count
        return changed >= 2 && nonEmpty > 0 && changed * 2 >= nonEmpty
    }

    var body: some View {
        let allLines = lines
        let clamped = !showAll && allLines.count > Self.collapsedLineCount
        let shown = clamped ? Array(allLines.prefix(Self.collapsedLineCount)) : allLines

        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Image(systemName: "chevron.left.forwardslash.chevron.right")
                    .font(.system(size: 8, weight: .medium))
                    .foregroundStyle(Theme.inkTertiary)
                Text(title)
                    .font(Theme.mono(10))
                    .foregroundStyle(Theme.inkSecondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 8)
                Text("\(allLines.count) line\(allLines.count == 1 ? "" : "s")")
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
                    .monospacedDigit()
                if allLines.count > Self.collapsedLineCount {
                    Button(showAll ? "Show less" : "Show all") {
                        withAnimation(.easeOut(duration: 0.15)) { showAll.toggle() }
                    }
                    .buttonStyle(.tactile)
                    .font(Theme.body(10, weight: .medium))
                    .foregroundStyle(Theme.accent)
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(Theme.sidebarSelection)

            Divider().overlay(Theme.hairline)

            Group {
                if isDiff {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(shown.enumerated()), id: \.offset) { _, line in
                            diffLine(line)
                        }
                    }
                    .padding(.vertical, 6)
                } else {
                    Text(shown.joined(separator: "\n"))
                        .font(Theme.mono(11))
                        .foregroundStyle(Theme.ink)
                        .textSelection(.enabled)
                        .lineSpacing(2)
                        .padding(10)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.card.opacity(0.6))
        }
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .strokeBorder(Theme.hairline, lineWidth: 1)
        )
    }

    private var title: String {
        filename ?? language ?? "Code"
    }

    @ViewBuilder private func diffLine(_ line: String) -> some View {
        let kind = diffKind(line)
        Text(line.isEmpty ? " " : line)
            .font(Theme.mono(11))
            .fontWeight(kind == .hunk ? .semibold : .regular)
            .foregroundStyle(diffForeground(kind))
            .textSelection(.enabled)
            .padding(.horizontal, 10)
            .padding(.vertical, 1)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(diffBackground(kind))
    }

    private enum DiffKind { case addition, deletion, hunk, context }

    private func diffKind(_ line: String) -> DiffKind {
        if line.hasPrefix("@@") { return .hunk }
        if line.hasPrefix("+") { return .addition }
        if line.hasPrefix("-") { return .deletion }
        return .context
    }

    private func diffForeground(_ kind: DiffKind) -> Color {
        switch kind {
        case .addition: return Theme.synced
        case .deletion: return Theme.live
        case .hunk: return Theme.accent
        case .context: return Theme.ink
        }
    }

    private func diffBackground(_ kind: DiffKind) -> Color {
        switch kind {
        case .addition: return Theme.synced.opacity(0.10)
        case .deletion: return Theme.live.opacity(0.10)
        case .hunk: return Theme.accent.opacity(0.08)
        case .context: return .clear
        }
    }
}

/// Hover copy affordance with checkmark feedback.
struct CopyIconButton: View {
    let text: String
    var help = "Copy markdown"

    @State private var copied = false

    var body: some View {
        Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            copied = true
            Task {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                copied = false
            }
        } label: {
            Image(systemName: copied ? "checkmark" : "doc.on.doc")
                .font(.system(size: 10, weight: .medium))
                .foregroundStyle(copied ? Theme.synced : Theme.inkSecondary)
                .frame(width: 14, height: 14)
        }
        .buttonStyle(.tactile)
        .padding(4)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 6, style: .continuous)
                .strokeBorder(Theme.hairline, lineWidth: 1)
        )
        .help(help)
        .accessibilityLabel(help)
    }
}
