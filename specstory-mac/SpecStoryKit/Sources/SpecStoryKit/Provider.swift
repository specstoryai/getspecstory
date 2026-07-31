import Foundation

/// The agents the specstory CLI can track, keyed by the CLI's registry IDs.
public enum Provider: String, CaseIterable, Codable, Sendable, Identifiable {
    case antigravity
    case claude
    case codex
    case copilotide
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
        case .copilotide: return "VS Code Copilot"
        case .cursor: return "Cursor CLI"
        case .cursoride: return "Cursor"
        case .deepseek: return "DeepSeek"
        case .droid: return "Droid"
        case .gemini: return "Gemini"
        }
    }

    /// IDE-hosted providers versus terminal agents; the Providers panel
    /// groups by this.
    public var isIDE: Bool {
        switch self {
        case .cursoride, .copilotide: return true
        default: return false
        }
    }

    /// Copilot capture happens through the SpecStory VS Code extension; the
    /// bundled CLI has no copilotide provider, so health checks and watch
    /// children skip it.
    public var watchableByCLI: Bool {
        self != .copilotide
    }

    /// Registry IDs are matched case-insensitively, like the CLI does.
    /// Cloud metadata carries display-style agent names ("Claude Code",
    /// "Codex Cli", "Factory Droid CLI"), so those map back too.
    public init?(providerID: String) {
        let normalized = providerID.lowercased()
        if let direct = Provider(rawValue: normalized) {
            self = direct
            return
        }
        // Collapse to letters only so "Codex CLI", "codex-cli", and
        // "Codex Cli" all land in one key.
        let letters = normalized.filter { $0.isLetter }
        switch letters {
        case "claudecode": self = .claude
        case "codexcli": self = .codex
        case "cursorcli": self = .cursor
        case "cursoride": self = .cursoride
        case "geminicli": self = .gemini
        case "antigravitycli": self = .antigravity
        case "deepseektui": self = .deepseek
        case "droidcli", "factorydroid", "factorydroidcli": self = .droid
        case "copilot", "githubcopilot", "vscodecopilot", "vscodecopilotide": self = .copilotide
        default: return nil
        }
    }
}
