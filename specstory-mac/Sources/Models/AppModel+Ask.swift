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
            askLocalFallback(query: query)
            return
        }

        askStreaming = true
        var assistant = AskMessage(role: .assistant, text: "", status: "Searching your sessions...")
        askMessages.append(assistant)
        let index = askMessages.count - 1

        askTask = Task { [weak self] in
            guard let self else { return }
            var pendingText = ""
            var lastFlush = Date.distantPast

            @MainActor func flush(force: Bool = false) {
                guard force || Date().timeIntervalSince(lastFlush) > 0.016 else { return }
                lastFlush = Date()
                assistant.text = pendingText
                self.askMessages[index] = assistant
            }

            do {
                let stream = self.chatClient.ask(
                    query: query,
                    chatSessionID: self.chatSessionID,
                    projectIDs: nil, timeFilter: nil, agentNames: nil
                )
                for try await event in stream {
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
                flush(force: true)
            } catch {
                assistant.failed = true
                assistant.status = nil
                if pendingText.isEmpty {
                    pendingText = self.friendlyCloudError(error)
                }
                flush(force: true)
            }
            self.askStreaming = false
        }
    }

    /// Signed-out experience: local FTS results plus a sign-in nudge.
    private func askLocalFallback(query: String) {
        var sources = [ChatSource]()
        if let reader = indexReader, let locals = try? reader.search(query, limit: 8) {
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
        askStreaming = false
        askMessages = []
        chatSessionID = nil
    }
}
