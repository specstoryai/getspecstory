import Foundation

/// The agents the specstory CLI can track, keyed by the CLI's registry IDs.
public enum Provider: String, CaseIterable, Codable, Sendable, Identifiable {
    case antigravity
    case claude
    case codex
    case cursor
    case cursoride
    case deepseek
    case droid
    case gemini

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .antigravity: return "Antigravity"
        case .claude: return "Claude Code"
        case .codex: return "Codex"
        case .cursor: return "Cursor CLI"
        case .cursoride: return "Cursor"
        case .deepseek: return "DeepSeek"
        case .droid: return "Droid"
        case .gemini: return "Gemini"
        }
    }

    /// Registry IDs are matched case-insensitively, like the CLI does.
    public init?(providerID: String) {
        self.init(rawValue: providerID.lowercased())
    }
}
