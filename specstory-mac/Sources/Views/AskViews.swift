import SwiftUI
import SpecStoryKit

/// The "Ask anything" input, Granola style: a solid white pill with chips for
/// @ context references, sitting on a paper gradient so feed content fades
/// out underneath instead of showing through.
struct AskBar: View {
    @ObservedObject var model: AppModel
    @ObservedObject var mention: MentionState
    @FocusState private var focused: Bool

    init(model: AppModel) {
        self.model = model
        self.mention = model.askMention
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if mention.hasChips {
                MentionChipRow(state: mention)
                    .padding(.horizontal, 16)
                    .padding(.top, 10)
                    .padding(.bottom, 2)
            }
            HStack(spacing: 10) {
                Image(systemName: "sparkles")
                    .font(.system(size: 13))
                    .foregroundStyle(Theme.accent)
                TextField(model.askStreaming ? "Answering..." : "Ask anything about your sessions", text: $mention.text)
                    .textFieldStyle(.plain)
                    .font(Theme.body(13))
                    .focused($focused)
                    .mentionTextFieldSupport(mention, candidates: candidatesProvider)
                    .onSubmit {
                        if mention.consumeReturn(candidates: candidatesProvider()) { return }
                        model.submitAsk()
                    }
                Text("@ to focus")
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
                if !mention.text.isEmpty {
                    Button {
                        if mention.consumeReturn(candidates: candidatesProvider()) { return }
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
            .padding(.vertical, 12)
        }
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(Theme.accent.opacity(0.35), lineWidth: 1.5)
        )
        .shadow(color: .black.opacity(0.08), radius: 14, y: 5)
        .popover(
            isPresented: Binding(
                get: { mention.popoverShown },
                set: { mention.popoverShown = $0 }
            ),
            attachmentAnchor: .rect(.bounds), arrowEdge: .top
        ) {
            MentionTypeaheadPopover(state: mention, candidates: candidatesProvider())
        }
        .padding(.horizontal, 40)
        .padding(.bottom, 16)
        .frame(maxWidth: Theme.feedWidth)
    }

    private func candidatesProvider() -> [MentionItem] {
        mention.candidatesFromApp(cloudProjects: model.cloudProjects)
    }
}

/// Paper-colored gradient that fades feed content out behind the Ask bar.
struct AskBarBackdrop: View {
    var body: some View {
        LinearGradient(
            stops: [
                .init(color: Theme.paper.opacity(0), location: 0),
                .init(color: Theme.paper.opacity(0.85), location: 0.35),
                .init(color: Theme.paper, location: 0.7),
            ],
            startPoint: .top, endPoint: .bottom
        )
        .frame(height: 110)
        .allowsHitTesting(false)
    }
}

/// The Chat panel: past conversations (cloud parity) plus the live thread.
struct ChatPanelView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Text("Chat")
                    .font(Theme.display(24))
                    .foregroundStyle(Theme.ink)
                Spacer()
                if !model.chatThreads.isEmpty {
                    Menu {
                        ForEach(model.chatThreads) { thread in
                            Button {
                                Task { await model.openChatThread(thread) }
                            } label: {
                                Text(threadLabel(thread))
                            }
                        }
                    } label: {
                        Label("History", systemImage: "clock.arrow.circlepath")
                            .font(Theme.body(12))
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                }
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
                            if model.chatThreads.isEmpty {
                                emptyState.padding(.top, 100)
                            } else {
                                historyList
                            }
                        }
                        ForEach(model.askMessages) { message in
                            AskMessageView(model: model, message: message)
                                .id(message.id)
                        }
                    }
                    .padding(.horizontal, 28)
                    .padding(.bottom, 110)
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
            ZStack(alignment: .bottom) {
                AskBarBackdrop()
                AskBar(model: model)
            }
        }
        .task {
            await model.refreshChatThreads()
        }
    }

    /// Past conversations as cards, like the cloud chat home.
    private var historyList: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Past conversations")
                .font(Theme.body(12, weight: .semibold))
                .foregroundStyle(Theme.inkTertiary)
                .textCase(.uppercase)
                .kerning(0.4)
            ForEach(model.chatThreads) { thread in
                ChatThreadRow(model: model, thread: thread)
            }
        }
        .padding(.top, 6)
    }

    private func threadLabel(_ thread: ChatThreadSummary) -> String {
        let title = thread.title?.isEmpty == false ? thread.title! : "Untitled chat"
        return String(title.prefix(60))
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "sparkles")
                .font(.system(size: 30, weight: .light))
                .foregroundStyle(Theme.accent)
            Text("Ask your coding history anything")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("\"How did I fix the auth race in March?\" \"Which sessions touched the billing worker?\" Answers cite the sessions they came from, and you can resume any of them. Type @ to focus on a project, agent, or time window.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 440)
        }
        .frame(maxWidth: .infinity)
    }
}

/// One past conversation row with open and delete.
struct ChatThreadRow: View {
    @ObservedObject var model: AppModel
    let thread: ChatThreadSummary

    @State private var hovering = false

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "bubble.left.and.text.bubble.right")
                .font(.system(size: 13))
                .foregroundStyle(Theme.accent)
            VStack(alignment: .leading, spacing: 2) {
                Text(thread.title?.isEmpty == false ? thread.title! : "Untitled chat")
                    .font(Theme.body(13, weight: .medium))
                    .foregroundStyle(Theme.ink)
                    .lineLimit(1)
                if let updated = thread.updatedAt ?? thread.createdAt,
                   let date = CloudDate.parse(updated) {
                    Text(date.formatted(.relative(presentation: .named)))
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                }
            }
            Spacer()
            if hovering {
                Button {
                    Task { await model.deleteChatThread(thread) }
                } label: {
                    Image(systemName: "trash")
                        .font(.system(size: 11))
                        .foregroundStyle(Theme.inkTertiary)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Delete chat")
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .contentShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
        .cardChrome(hovering: hovering)
        .onHover { hovering = $0 }
        .onTapGesture {
            Task { await model.openChatThread(thread) }
        }
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
