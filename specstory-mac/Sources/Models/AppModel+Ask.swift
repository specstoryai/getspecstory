import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Ask anything (RAG over synced chats)

    var canAskCloud: Bool {
        if case .signedIn = authState { return true }
        return false
    }

    func submitAsk() {
        let query = askQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty, !askStreaming else { return }
        askQuery = ""
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
                    projectIDs: nil, timeFilter: nil, agentNames: nil
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

    func openAskSource(_ source: ChatSource) {
        revealSession(id: source.sessionClientID)
    }

    func clearAskThread() {
        askTask?.cancel()
        askTask = nil
        askStreaming = false
        askMessages = []
        chatSessionID = nil
    }
}
