import Foundation

/// One NDJSON line from `specstory watch --json`.
///
/// The stream can interleave slog text lines with JSON records, so parsing is
/// line-tolerant: non-JSON lines return nil and should be re-leveled into app
/// logging by the caller.
public struct WatchEvent: Codable, Equatable, Sendable {
    public enum Action: String, Codable, Sendable {
        case created
        case updated
    }

    public let timestamp: String
    public let action: Action
    public let sessionID: String
    public let startTime: String?
    public let endTime: String?
    public let provider: String
    public let markdownSize: Int?
    public let totalUserPrompts: Int?
    public let agentActivity: Int?
    /// Absent when running with --only-cloud-sync.
    public let markdownFile: String?

    enum CodingKeys: String, CodingKey {
        case timestamp
        case action
        case sessionID = "session_id"
        case startTime = "start_time"
        case endTime = "end_time"
        case provider
        case markdownSize = "markdown_size"
        case totalUserPrompts = "total_user_prompts"
        case agentActivity = "agent_activity"
        case markdownFile = "markdown_file"
    }

    /// Parses a single stdout line; nil for slog noise or partial lines.
    public static func parse(line: String) -> WatchEvent? {
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasPrefix("{"), let data = trimmed.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(WatchEvent.self, from: data)
    }
}
