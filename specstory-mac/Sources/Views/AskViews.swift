import SwiftUI
import SpecStoryKit

/// Floating "Ask anything" input pinned to the bottom of the feed, Granola
/// style. Submitting routes to the Chat panel with a streaming answer.
struct AskBar: View {
    @ObservedObject var model: AppModel
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "sparkles")
                .font(.system(size: 13))
                .foregroundStyle(Theme.accent)
            TextField(model.askStreaming ? "Answering..." : "Ask anything about your sessions", text: $model.askQuery)
                .textFieldStyle(.plain)
                .font(Theme.body(13))
                .focused($focused)
                .onSubmit { model.submitAsk() }
            if !model.askQuery.isEmpty {
                Button {
                    model.submitAsk()
                } label: {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.system(size: 18))
                        .foregroundStyle(model.askStreaming ? Theme.inkTertiary : Theme.accent)
                }
                .buttonStyle(.plain)
                .disabled(model.askStreaming)
                .accessibilityLabel("Ask")
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Theme.hairline))
        .shadow(color: .black.opacity(0.10), radius: 12, y: 4)
        .padding(.horizontal, 40)
        .padding(.bottom, 18)
        .frame(maxWidth: Theme.feedWidth)
    }
}

/// The Chat panel: ask thread with streamed answers and source pills.
struct ChatPanelView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Chat")
                    .font(Theme.display(24))
                    .foregroundStyle(Theme.ink)
                Spacer()
                if !model.askMessages.isEmpty {
                    Button("New chat") { model.clearAskThread() }
                        .buttonStyle(.plain)
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.accent)
                }
            }
            .padding(.horizontal, 28)
            .padding(.top, 24)
            .padding(.bottom, 12)

            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        if model.askMessages.isEmpty {
                            emptyState.padding(.top, 100)
                        }
                        ForEach(model.askMessages) { message in
                            AskMessageView(model: model, message: message)
                                .id(message.id)
                        }
                    }
                    .padding(.horizontal, 28)
                    .padding(.bottom, 90)
                    .frame(maxWidth: Theme.feedWidth)
                    .frame(maxWidth: .infinity)
                }
                .onChange(of: model.askMessages.last?.text) { _ in
                    if let last = model.askMessages.last {
                        proxy.scrollTo(last.id, anchor: .bottom)
                    }
                }
            }

            Spacer(minLength: 0)
        }
        .background(Theme.paper)
        .overlay(alignment: .bottom) {
            AskBar(model: model)
        }
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "sparkles")
                .font(.system(size: 30, weight: .light))
                .foregroundStyle(Theme.accent)
            Text("Ask your coding history anything")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("\"How did I fix the auth race in March?\" \"Which sessions touched the billing worker?\" Answers cite the sessions they came from, and you can resume any of them.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 420)
        }
        .frame(maxWidth: .infinity)
    }
}

struct AskMessageView: View {
    @ObservedObject var model: AppModel
    let message: AskMessage

    var body: some View {
        switch message.role {
        case .user:
            HStack {
                Spacer(minLength: 60)
                Text(message.text)
                    .font(Theme.body(13))
                    .foregroundStyle(Theme.ink)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 9)
                    .background(Theme.sidebarSelection, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            }
        case .assistant:
            VStack(alignment: .leading, spacing: 10) {
                if let status = message.status {
                    HStack(spacing: 6) {
                        ProgressView().controlSize(.mini)
                        Text(status)
                            .font(Theme.body(11))
                            .foregroundStyle(Theme.inkSecondary)
                    }
                }
                if !message.sources.isEmpty {
                    SourcePillRow(model: model, sources: message.sources)
                }
                if !message.text.isEmpty {
                    Text(renderedAnswer)
                        .font(Theme.body(13))
                        .foregroundStyle(message.failed ? Theme.inkSecondary : Theme.ink)
                        .textSelection(.enabled)
                        .lineSpacing(3)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// Inline markdown with [chunk:ID] citations reduced to superscript dots.
    private var renderedAnswer: AttributedString {
        var text = message.text
        text = text.replacingOccurrences(
            of: #"\[chunk:[^\]]+\]"#, with: "", options: .regularExpression
        )
        if let attributed = try? AttributedString(
            markdown: text,
            options: AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        ) {
            return attributed
        }
        return AttributedString(text)
    }
}

/// Source sessions the answer is grounded in; each deep-links to the session
/// with Resume one click away. The killer loop.
struct SourcePillRow: View {
    @ObservedObject var model: AppModel
    let sources: [ChatSource]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(Array(uniqueSources.enumerated()), id: \.offset) { _, source in
                    Button {
                        model.openAskSource(source)
                    } label: {
                        HStack(spacing: 6) {
                            Image(systemName: "text.bubble")
                                .font(.system(size: 10))
                            VStack(alignment: .leading, spacing: 1) {
                                Text(source.userTitle ?? source.sessionName)
                                    .font(Theme.body(11, weight: .medium))
                                    .lineLimit(1)
                                if let project = source.projectName {
                                    Text(project)
                                        .font(Theme.body(9))
                                        .foregroundStyle(Theme.inkTertiary)
                                }
                            }
                        }
                        .padding(.horizontal, 10)
                        .padding(.vertical, 6)
                        .background(Theme.card, in: Capsule())
                        .overlay(Capsule().strokeBorder(Theme.hairline))
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    /// The stream repeats sessions across exchanges; show each session once.
    private var uniqueSources: [ChatSource] {
        var seen = Set<String>()
        return sources.filter { seen.insert($0.sessionClientID).inserted }
    }
}
