import Foundation

/// One session's entry in `{projectPath}/.specstory/statistics.json`, written
/// by the CLI (pkg/session/statistics.go). All fields optional for drift
/// tolerance: a partially written entry should not sink the whole file.
public struct SessionStats: Codable, Equatable, Sendable {
    public let userMessageCount: Int?
    public let agentMessageCount: Int?
    /// ISO 8601 strings as written by the CLI; kept raw for display formatting.
    public let startTimestamp: String?
    public let endTimestamp: String?
    public let markdownSizeBytes: Int?
    public let provider: String?
    public let lastUpdated: String?

    enum CodingKeys: String, CodingKey {
        case userMessageCount = "user_message_count"
        case agentMessageCount = "agent_message_count"
        case startTimestamp = "start_timestamp"
        case endTimestamp = "end_timestamp"
        case markdownSizeBytes = "markdown_size_bytes"
        case provider
        case lastUpdated = "last_updated"
    }

    public init(
        userMessageCount: Int?,
        agentMessageCount: Int?,
        startTimestamp: String?,
        endTimestamp: String?,
        markdownSizeBytes: Int?,
        provider: String?,
        lastUpdated: String?
    ) {
        self.userMessageCount = userMessageCount
        self.agentMessageCount = agentMessageCount
        self.startTimestamp = startTimestamp
        self.endTimestamp = endTimestamp
        self.markdownSizeBytes = markdownSizeBytes
        self.provider = provider
        self.lastUpdated = lastUpdated
    }
}

/// `{projectPath}/.specstory/.project.json`, written by the CLI
/// (pkg/utils/project_identity.go). `workspace_id` is always present; `git_id`
/// only when the project has a git remote.
public struct ProjectInfo: Codable, Equatable, Sendable {
    public let workspaceID: String
    public let workspaceIDAt: String?
    public let gitID: String?
    public let gitIDAt: String?
    public let projectName: String?

    enum CodingKeys: String, CodingKey {
        case workspaceID = "workspace_id"
        case workspaceIDAt = "workspace_id_at"
        case gitID = "git_id"
        case gitIDAt = "git_id_at"
        case projectName = "project_name"
    }

    public init(
        workspaceID: String,
        workspaceIDAt: String?,
        gitID: String?,
        gitIDAt: String?,
        projectName: String?
    ) {
        self.workspaceID = workspaceID
        self.workspaceIDAt = workspaceIDAt
        self.gitID = gitID
        self.gitIDAt = gitIDAt
        self.projectName = projectName
    }
}

/// Reads the CLI's per-project sidecar files. Both readers return nil on
/// missing or corrupt files and never throw: these files are CLI-owned and may
/// be mid-write or absent at any time.
public enum StatsReader {
    /// Session statistics from `{projectPath}/.specstory/statistics.json`
    /// (the output dir root, not inside history/), keyed by session id.
    public static func statistics(projectPath: String) -> [String: SessionStats]? {
        // Matches the CLI's StatisticsFile envelope: {"sessions": {...}}.
        struct StatisticsFile: Codable {
            let sessions: [String: SessionStats]?
        }
        guard let file: StatisticsFile = decode(projectPath: projectPath, relative: "statistics.json") else {
            return nil
        }
        return file.sessions ?? [:]
    }

    /// Project identity from `{projectPath}/.specstory/.project.json`.
    public static func projectInfo(projectPath: String) -> ProjectInfo? {
        decode(projectPath: projectPath, relative: ".project.json")
    }

    private static func decode<T: Decodable>(projectPath: String, relative: String) -> T? {
        let url = URL(fileURLWithPath: TildeExpansion.expand(projectPath))
            .appendingPathComponent(".specstory")
            .appendingPathComponent(relative)
        guard let data = try? Data(contentsOf: url) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
}
