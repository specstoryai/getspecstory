import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Ask anything (RAG over synced chats)

    var canAskCloud: Bool {
        if case .signedIn = authState { return true }
        return false
    }

    func submitAsk() {
        let query = askMention.text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty, !askStreaming else { return }
        askMention.text = ""
        askMention.dismissPopover()
        askMessages.append(AskMessage(role: .user, text: query))
        panelMode = .chat

        guard canAskCloud else {
            Task { await askLocalFallback(query: query) }
            return
        }

        askStreaming = true
        var assistant = AskMessage(role: .assistant, text: "", status: "Searching your sessions...")
        askMessages.append(assistant)
        let assistantID = assistant.id

        askTask = Task { [weak self] in
            guard let self else { return }
            var pendingText = ""
            var lastFlush = Date.distantPast

            // The thread can be cleared mid-stream; locate the message by
            // identity every flush and stop writing once it is gone.
            @MainActor func flush(force: Bool = false) {
                guard force || Date().timeIntervalSince(lastFlush) > 0.016 else { return }
                lastFlush = Date()
                guard let slot = self.askMessages.firstIndex(where: { $0.id == assistantID }) else { return }
                assistant.text = pendingText
                self.askMessages[slot] = assistant
            }

            do {
                let stream = self.chatClient.ask(
                    query: query,
                    chatSessionID: self.chatSessionID,
                    projectIDs: self.askMention.projectIDsParam,
                    timeFilter: self.askMention.timeFilterParam,
                    agentNames: self.askMention.agentNamesParam
                )
                for try await event in stream {
                    guard !Task.isCancelled else { break }
                    switch event {
                    case .start(let chatSessionID):
                        self.chatSessionID = chatSessionID
                    case .status(let status):
                        assistant.status = status
                        flush(force: true)
                    case .queryRewritten(let rewritten):
                        assistant.status = "Searched for: \(rewritten)"
                        flush(force: true)
                    case .sources(let sources):
                        assistant.sources = sources
                        assistant.status = nil
                        flush(force: true)
                    case .chunk(let delta):
                        assistant.status = nil
                        pendingText += delta
                        flush()
                    case .end:
                        break
                    case .failure(let message):
                        assistant.failed = true
                        assistant.status = nil
                        if pendingText.isEmpty {
                            pendingText = "Something went wrong answering that: \(message)"
                        }
                    }
                }
                assistant.status = nil
                flush(force: true)
            } catch {
                if !Task.isCancelled {
                    assistant.failed = true
                    assistant.status = nil
                    if pendingText.isEmpty {
                        pendingText = self.friendlyCloudError(error)
                    }
                    flush(force: true)
                }
            }
            if !Task.isCancelled {
                self.askStreaming = false
            }
        }
    }

    /// Signed-out experience: local FTS results plus a sign-in nudge.
    private func askLocalFallback(query: String) async {
        var sources = [ChatSource]()
        do {
            let locals = await indexBox.search(query, limit: 8)
            sources = locals.map { local in
                ChatSource(
                    chunkID: "", exchangeID: "", exchangeChunkID: nil,
                    sessionID: local.sessionID, sessionClientID: local.sessionID,
                    projectID: "", sessionName: local.title, userTitle: nil,
                    sessionSummary: nil,
                    projectName: SessionItem.folderName(of: local.projectPath),
                    projectIcon: nil, projectColor: nil
                )
            }
        }
        let text = sources.isEmpty
            ? "No local sessions matched that. Sign in to SpecStory Cloud to ask questions across all your synced chats."
            : "Here are local sessions that match. Sign in to SpecStory Cloud for AI answers grounded in your chats."
        askMessages.append(AskMessage(role: .assistant, text: text, sources: sources))
    }

    /// Opens a cited session from Chat: switches to Home (the detail pane
    /// lives there) and falls back to a cloud-addressed item when the session
    /// is not in the merged feed yet.
    // MARK: Prompt and response actions

    /// Copy the raw prompt or answer text.
    func copyMessageText(_ message: AskMessage) {
        MarkdownOpener.copyToPasteboard(message.text)
        showToast(message.role == .user ? "Prompt copied" : "Answer copied")
    }

    /// Edit a prompt: the conversation forks at that point. Everything from
    /// the message on is removed, its text lands back in the ask bar for
    /// editing, and the next submit starts a fresh server thread so the
    /// rewritten conversation is not haunted by the replaced turns.
    func editPrompt(_ message: AskMessage) {
        guard message.role == .user else { return }
        askTask?.cancel()
        askStreaming = false
        if let index = askMessages.firstIndex(where: { $0.id == message.id }) {
            askMessages.removeSubrange(index...)
        }
        chatSessionID = nil
        askMention.text = message.text
        requestAskFocus()
    }

    /// Regenerate from a prompt: fork exactly like editPrompt, then resend
    /// the same text immediately.
    func regeneratePrompt(_ message: AskMessage) {
        guard message.role == .user, !askStreaming else { return }
        editPrompt(message)
        submitAsk()
    }

    /// Response follow-up affordance: park the caret in the ask bar.
    func askFollowUp() {
        panelMode = .chat
        requestAskFocus()
    }

    func requestAskFocus() {
        askFocusTick += 1
    }

    func openAskSource(_ source: ChatSource) {
        panelMode = .home
        if let item = allItems.first(where: { $0.clientID == source.sessionClientID }) {
            openSession(item)
            return
        }
        if let live = liveSessions[source.sessionClientID] {
            openSession(live.asSessionItem)
            return
        }
        // Not merged yet: address it directly by client ids; the detail view
        // loads markdown from the cloud with exactly these two fields.
        let cloud = CloudSession(
            id: source.sessionID, clientId: source.sessionClientID,
            projectId: source.projectID,
            name: source.userTitle ?? source.sessionName
        )
        openSession(SessionItem(cloud: cloud, projectName: source.projectName))
    }

    func clearAskThread() {
        askTask?.cancel()
        askTask = nil
        askStreaming = false
        askMessages = []
        chatSessionID = nil
        askMention.clearAll()
    }

    // MARK: Chat history (cloud parity browsing)

    func refreshChatThreads() async {
        guard canAskCloud else {
            chatThreads = []
            return
        }
        chatThreads = (try? await chatClient.chats(limit: 50)) ?? []
    }

    /// Loads a past thread into the panel, mapping stored messages back to
    /// the ask transcript shape.
    func openChatThread(_ summary: ChatThreadSummary) async {
        askTask?.cancel()
        askStreaming = false
        chatSessionID = summary.id
        guard let thread = try? await chatClient.chat(id: summary.id) else {
            showToast("Could not load that chat")
            return
        }
        var messages = [AskMessage]()
        for message in thread.messages {
            if let query = message.query, !query.isEmpty {
                messages.append(AskMessage(role: .user, text: query))
            }
            if let response = message.response, !response.isEmpty {
                messages.append(AskMessage(role: .assistant, text: response, sources: message.sources))
            }
        }
        askMessages = messages
        panelMode = .chat
    }

    func deleteChatThread(_ summary: ChatThreadSummary) async {
        try? await chatClient.deleteChat(id: summary.id)
        chatThreads.removeAll { $0.id == summary.id }
        if chatSessionID == summary.id {
            clearAskThread()
        }
    }
}
