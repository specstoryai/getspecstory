import SwiftUI
import SpecStoryKit

/// One exchange: the tinted user prompt bubble plus the agent's unbubbled
/// output. Equatable so scrolled-past cards skip re-rendering when unrelated
/// state changes.
struct ExchangeCardView: View, Equatable {
    let exchange: SessionTranscript.Exchange
    let number: Int
    let visible: Set<TranscriptElement>

    @State private var hoveringResponse = false

    static func == (lhs: ExchangeCardView, rhs: ExchangeCardView) -> Bool {
        lhs.exchange == rhs.exchange && lhs.number == rhs.number && lhs.visible == rhs.visible
    }

    var body: some View {
        let agentShown = visibleAgentSegments

        VStack(alignment: .leading, spacing: 12) {
            if promptVisible {
                PromptBubbleView(exchange: exchange, number: number)
            }

            if !agentShown.isEmpty {
                responseView(agentShown)
            }

            if !promptVisible, agentShown.isEmpty, hiddenCount > 0 {
                Text("\(hiddenCount) element\(hiddenCount == 1 ? "" : "s") hidden by filters")
                    .font(Theme.body(11))
                    .italic()
                    .foregroundStyle(Theme.inkTertiary)
                    .padding(.vertical, 2)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    // MARK: Filtering

    private var hasPromptContent: Bool {
        exchange.userSegments.contains { segment in
            switch segment {
            case .prose, .code: return true
            default: return false
            }
        }
    }

    private var promptVisible: Bool {
        visible.contains(.prompts) && hasPromptContent
    }

    private var visibleAgentSegments: [(offset: Int, segment: SessionTranscript.Segment)] {
        Array(exchange.agentSegments.enumerated())
            .filter { visible.contains(TranscriptElement.element(for: $0.element)) }
            .map { (offset: $0.offset, segment: $0.element) }
    }

    private var hiddenCount: Int {
        let hiddenAgent = exchange.agentSegments.count - visibleAgentSegments.count
        let hiddenPrompt = (!visible.contains(.prompts) && hasPromptContent) ? 1 : 0
        return hiddenAgent + hiddenPrompt
    }

    // MARK: Response

    private func responseView(_ segments: [(offset: Int, segment: SessionTranscript.Segment)]) -> some View {
        ZStack(alignment: .topTrailing) {
            VStack(alignment: .leading, spacing: 10) {
                ForEach(segments, id: \.offset) { entry in
                    segmentView(entry.segment)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 2)

            CopyIconButton(text: exchange.rawAgentMarkdown, help: "Copy response markdown")
                .opacity(hoveringResponse ? 1 : 0)
        }
        .onHover { hoveringResponse = $0 }
    }

    @ViewBuilder private func segmentView(_ segment: SessionTranscript.Segment) -> some View {
        switch segment {
        case .prose(let text):
            ProseBlocksView(text: text)
        case .code(let language, let filename, let content):
            CodeBlockView(language: language, filename: filename, content: content)
        case .thinking(let text):
            ThinkingSegmentView(text: text)
        case .toolUse(let type, let name, let summary, let body):
            ToolPillView(type: type, name: name, summary: summary, detail: body)
        case .roleHeader(let role, let model, let timestamp, let sidechain):
            RoleHeaderLine(role: role, model: model, timestamp: timestamp, sidechain: sidechain)
        }
    }
}

/// The user turn: accent-tinted bubble, "You" caption with timestamp, long
/// prompts clamped with Show more, hover copy of the raw prompt markdown.
struct PromptBubbleView: View {
    let exchange: SessionTranscript.Exchange
    let number: Int

    @State private var expanded = false
    @State private var hovering = false

    /// Roughly seven text lines; content taller than this clamps.
    private static let collapsedHeight: CGFloat = 132

    private var contentSegments: [SessionTranscript.Segment] {
        exchange.userSegments.filter { segment in
            switch segment {
            case .prose, .code: return true
            default: return false
            }
        }
    }

    private var sidechain: Bool {
        exchange.userSegments.contains { segment in
            if case .roleHeader(_, _, _, true) = segment { return true }
            return false
        }
    }

    private var needsClamp: Bool {
        let raw = exchange.rawUserMarkdown
        if raw.count > 700 { return true }
        return raw.reduce(into: 0) { count, character in
            if character == "\n" { count += 1 }
        } > 7
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Text("You")
                    .font(Theme.body(10.5, weight: .semibold))
                    .foregroundStyle(Theme.accent)
                if sidechain {
                    Text("subagent")
                        .font(Theme.body(9, weight: .semibold))
                        .foregroundStyle(Theme.inkTertiary)
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(Capsule().fill(Theme.sidebarSelection))
                }
                if let time = TranscriptFormat.timeLabel(exchange.timestamp) {
                    Text(time)
                        .font(Theme.body(10))
                        .foregroundStyle(Theme.inkTertiary)
                }
                Spacer()
                Text("#\(number)")
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
                    .monospacedDigit()
                CopyIconButton(text: exchange.rawUserMarkdown, help: "Copy prompt markdown")
                    .opacity(hovering ? 1 : 0)
            }
            .frame(height: 18)

            promptContent
                .frame(maxHeight: expanded || !needsClamp ? nil : Self.collapsedHeight, alignment: .top)
                .clipped()

            if needsClamp {
                Button(expanded ? "Show less" : "Show more") {
                    withAnimation(.easeOut(duration: 0.15)) { expanded.toggle() }
                }
                .buttonStyle(.plain)
                .font(Theme.body(11, weight: .medium))
                .foregroundStyle(Theme.accent)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Theme.accent.opacity(0.08))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(Theme.accent.opacity(0.25), lineWidth: 1)
        )
        .onHover { hovering = $0 }
    }

    private var promptContent: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(contentSegments.enumerated()), id: \.offset) { _, segment in
                switch segment {
                case .prose(let text):
                    ProseBlocksView(text: text)
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
