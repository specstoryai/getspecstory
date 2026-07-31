import Foundation
import SpecStoryKit

/// A session currently receiving watch events; feeds the pinned Live now
/// section and the menu bar.
struct LiveSession: Identifiable, Equatable {
    let sessionID: String
    var provider: String
    var projectPath: String
    var startedAt: Date
    var lastEventAt: Date
    var promptCount: Int
    var agentActivity: Int
    var markdownSize: Int
    var markdownFile: String?

    var id: String { sessionID }

    var projectName: String {
        SessionItem.folderName(of: projectPath) ?? "Unknown project"
    }

    init(projectPath: String, event: WatchEvent) {
        sessionID = event.sessionID
        provider = event.provider
        self.projectPath = projectPath
        startedAt = Date()
        lastEventAt = Date()
        promptCount = event.totalUserPrompts ?? 0
        agentActivity = event.agentActivity ?? 0
        markdownSize = event.markdownSize ?? 0
        markdownFile = event.markdownFile
    }

    mutating func apply(_ event: WatchEvent) {
        provider = event.provider
        lastEventAt = Date()
        promptCount = event.totalUserPrompts ?? promptCount
        agentActivity = event.agentActivity ?? agentActivity
        markdownSize = event.markdownSize ?? markdownSize
        markdownFile = event.markdownFile ?? markdownFile
    }

    /// Placeholder feed item so live sessions render with the standard card.
    var asSessionItem: SessionItem {
        SessionItem(
            local: IndexedSession(
                sessionID: sessionID, provider: provider, projectPath: projectPath,
                title: "Live session in \(projectName)", slug: nil,
                createdAt: startedAt, updatedAt: lastEventAt,
                userPromptCount: promptCount, markdownPath: markdownFile
            )
        )
    }
}

/// One message in the Ask conversation.
struct AskMessage: Identifiable, Equatable {
    enum Role { case user, assistant }

    let id = UUID()
    let role: Role
    var text: String
    var status: String?          // transient "Searching your sessions..." line
    var sources: [ChatSource] = []
    var failed = false
}

/// Provider health + notification preference row.
struct ProviderStatus: Identifiable, Equatable {
    let provider: Provider
    var healthy: Bool?           // nil while checking
    var notificationsOn: Bool

    var id: String { provider.id }
}
