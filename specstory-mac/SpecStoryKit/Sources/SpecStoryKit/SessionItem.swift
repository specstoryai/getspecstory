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
        title = SessionItem.humanizeTitle(local.title)
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
        title = SessionItem.humanizeTitle(cloud.displayTitle)
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
        title = SessionItem.humanizeTitle(cloud.userTitle ?? cloud.metadata.title ?? local.title)
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

    /// CLI slugs ("so-here-is-the") read poorly as titles; humanize unless the
    /// title already looks like prose.
    public static func humanizeTitle(_ raw: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return "Untitled session" }
        guard !trimmed.contains(" "), trimmed.contains("-") || trimmed.contains("_") else { return trimmed }
        let words = trimmed
            .replacingOccurrences(of: "_", with: "-")
            .split(separator: "-")
            .map(String.init)
        guard let first = words.first else { return trimmed }
        return ([first.prefix(1).uppercased() + first.dropFirst()] + words.dropFirst()).joined(separator: " ")
    }
}

/// How the feed clusters sessions into sections.
public enum FeedGrouping: String, CaseIterable, Codable, Sendable {
    case time
    case project
    case provider

    public var displayName: String {
        switch self {
        case .time: return "Time"
        case .project: return "Project"
        case .provider: return "Agent"
        }
    }
}

/// Ordering of sessions inside each section.
public enum FeedSort: String, CaseIterable, Codable, Sendable {
    case recent
    case longest
    case mostPrompts

    public var displayName: String {
        switch self {
        case .recent: return "Most recent"
        case .longest: return "Longest"
        case .mostPrompts: return "Most prompts"
        }
    }
}

extension SessionItem {
    /// Wall-clock session length when both ends are known.
    public var duration: TimeInterval? {
        guard let createdAt, let updatedAt, updatedAt > createdAt else { return nil }
        return updatedAt.timeIntervalSince(createdAt)
    }
}

/// A date-bucketed section of the feed (the Granola grouping).
public struct FeedSection: Identifiable, Equatable, Sendable {
    public let title: String      // "Today", "Yesterday", "Tue, Jul 29", "July 2026"
    public let items: [SessionItem]

    public var id: String { title }

    /// Sections the feed by the chosen grouping, ordering sessions inside
    /// each section by the chosen sort. Section order: time buckets stay
    /// chronological; project and provider sections order by most recent
    /// activity so active work floats up.
    public static func build(
        _ items: [SessionItem],
        grouping: FeedGrouping,
        sort: FeedSort,
        now: Date = Date(),
        calendar: Calendar = .current
    ) -> [FeedSection] {
        let sorted = apply(sort, to: items)
        switch grouping {
        case .time:
            // Time buckets need recency order to bucket correctly; re-sort
            // inside each bucket afterwards.
            let buckets = group(items.sorted { $0.sortDate > $1.sortDate }, now: now, calendar: calendar)
            return buckets.map { FeedSection(title: $0.title, items: apply(sort, to: $0.items)) }
        case .project:
            return sectioned(sorted, by: { $0.projectName ?? "No project" })
        case .provider:
            return sectioned(sorted, by: { Provider(providerID: $0.provider)?.displayName ?? $0.provider.capitalized })
        }
    }

    private static func apply(_ sort: FeedSort, to items: [SessionItem]) -> [SessionItem] {
        switch sort {
        case .recent:
            return items.sorted { $0.sortDate > $1.sortDate }
        case .longest:
            return items.sorted { ($0.duration ?? -1) > ($1.duration ?? -1) }
        case .mostPrompts:
            return items.sorted { ($0.promptCount ?? -1) > ($1.promptCount ?? -1) }
        }
    }

    private static func sectioned(_ items: [SessionItem], by key: (SessionItem) -> String) -> [FeedSection] {
        var order = [String]()
        var buckets = [String: [SessionItem]]()
        var latest = [String: Date]()
        for item in items {
            let section = key(item)
            if buckets[section] == nil { order.append(section) }
            buckets[section, default: []].append(item)
            latest[section] = max(latest[section] ?? .distantPast, item.sortDate)
        }
        let ranked = order.sorted { (latest[$0] ?? .distantPast) > (latest[$1] ?? .distantPast) }
        return ranked.map { FeedSection(title: $0, items: buckets[$0] ?? []) }
    }

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
