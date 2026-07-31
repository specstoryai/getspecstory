import Foundation

/// One row in the unified session feed: a local session, a cloud session, or
/// both merged. Merge identity is the client-generated session id (the CLI's
/// sessionID equals the cloud clientId) so the same session synced from this
/// machine collapses into a single item.
public struct SessionItem: Identifiable, Equatable, Sendable {
    public enum Origin: Equatable, Sendable {
        case localOnly
        case cloudOnly
        case both
    }

    public let clientID: String
    public let origin: Origin
    public let provider: String
    public let title: String
    public let projectPath: String?     // local working directory when known
    public let projectID: String?       // cloud project client id when synced
    public let projectName: String?
    public let machineName: String?     // cloud metadata; badge for other machines
    public let deviceID: String?
    public let createdAt: Date?
    public let updatedAt: Date?
    public let promptCount: Int?
    public let sessionDataSize: Int?    // > 0 means cloud resume material exists

    public var id: String { clientID }

    /// Feed ordering key: most recent activity wins.
    public var sortDate: Date {
        updatedAt ?? createdAt ?? .distantPast
    }

    /// A cloud-only session recorded on a different machine than this one.
    public func isFromOtherMachine(currentDeviceID: String?) -> Bool {
        guard origin == .cloudOnly, let deviceID, let currentDeviceID else { return false }
        return deviceID != currentDeviceID
    }

    public init(local: IndexedSession, promptCount: Int? = nil) {
        clientID = local.sessionID
        origin = .localOnly
        provider = local.provider
        title = local.title
        projectPath = local.projectPath.isEmpty ? nil : local.projectPath
        projectID = nil
        projectName = SessionItem.folderName(of: local.projectPath)
        machineName = nil
        deviceID = nil
        createdAt = local.createdAt
        updatedAt = local.updatedAt
        self.promptCount = promptCount ?? local.userPromptCount
        sessionDataSize = nil
    }

    public init(cloud: CloudSession, projectName: String?) {
        clientID = cloud.clientId
        origin = .cloudOnly
        provider = cloud.metadata.agentName ?? "unknown"
        title = cloud.displayTitle
        projectPath = nil
        projectID = cloud.projectId
        self.projectName = projectName
        machineName = cloud.metadata.machineName
        deviceID = cloud.metadata.deviceId
        createdAt = cloud.createdAtDate ?? cloud.startedAtDate
        updatedAt = cloud.endedAtDate ?? cloud.updatedAtDate
        promptCount = nil
        sessionDataSize = cloud.sessionDataSize
    }

    init(merging local: IndexedSession, with cloud: CloudSession, projectName: String?) {
        clientID = local.sessionID
        origin = .both
        provider = local.provider
        // Cloud carries user-editable titles; prefer them over the CLI slug.
        title = cloud.userTitle ?? cloud.metadata.title ?? local.title
        projectPath = local.projectPath.isEmpty ? nil : local.projectPath
        projectID = cloud.projectId
        self.projectName = projectName ?? SessionItem.folderName(of: local.projectPath)
        machineName = cloud.metadata.machineName
        deviceID = cloud.metadata.deviceId
        createdAt = local.createdAt ?? cloud.createdAtDate
        updatedAt = max(local.updatedAt ?? .distantPast, cloud.endedAtDate ?? cloud.updatedAtDate ?? .distantPast)
        promptCount = local.userPromptCount
        sessionDataSize = cloud.sessionDataSize
    }

    /// Merges local and cloud rows into one feed, local metadata winning on
    /// conflicts, sorted by most recent activity.
    public static func merge(
        local: [IndexedSession],
        cloud: [CloudSession],
        projectNames: [String: String] = [:]
    ) -> [SessionItem] {
        var cloudByClientID = [String: CloudSession]()
        for session in cloud {
            cloudByClientID[session.clientId] = session
        }

        var items = [SessionItem]()
        var seen = Set<String>()
        for localSession in local {
            if let match = cloudByClientID[localSession.sessionID] {
                items.append(SessionItem(merging: localSession, with: match, projectName: projectNames[match.projectId]))
            } else {
                items.append(SessionItem(local: localSession))
            }
            seen.insert(localSession.sessionID)
        }
        for session in cloud where !seen.contains(session.clientId) {
            items.append(SessionItem(cloud: session, projectName: projectNames[session.projectId]))
        }
        return items.sorted { $0.sortDate > $1.sortDate }
    }

    public static func folderName(of path: String) -> String? {
        let trimmed = path.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return nil }
        return URL(fileURLWithPath: trimmed).lastPathComponent
    }
}

/// A date-bucketed section of the feed (the Granola grouping).
public struct FeedSection: Identifiable, Equatable, Sendable {
    public let title: String      // "Today", "Yesterday", "Tue, Jul 29", "July 2026"
    public let items: [SessionItem]

    public var id: String { title }

    /// Groups items (already sorted descending) into display sections.
    public static func group(_ items: [SessionItem], now: Date = Date(), calendar: Calendar = .current) -> [FeedSection] {
        var order = [String]()
        var buckets = [String: [SessionItem]]()

        let dayFormatter = DateFormatter()
        dayFormatter.calendar = calendar
        dayFormatter.dateFormat = "EEE, MMM d"
        let monthFormatter = DateFormatter()
        monthFormatter.calendar = calendar
        monthFormatter.dateFormat = "MMMM yyyy"

        for item in items {
            let date = item.sortDate
            let title: String
            if calendar.isDate(date, inSameDayAs: now) {
                title = "Today"
            } else if let yesterday = calendar.date(byAdding: .day, value: -1, to: now),
                      calendar.isDate(date, inSameDayAs: yesterday) {
                title = "Yesterday"
            } else if let weekAgo = calendar.date(byAdding: .day, value: -7, to: now), date > weekAgo {
                title = dayFormatter.string(from: date)
            } else if date == .distantPast {
                title = "Earlier"
            } else {
                title = monthFormatter.string(from: date)
            }
            if buckets[title] == nil {
                order.append(title)
            }
            buckets[title, default: []].append(item)
        }
        return order.map { FeedSection(title: $0, items: buckets[$0] ?? []) }
    }
}
