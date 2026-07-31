import Foundation

/// Live getenv reads (ProcessInfo caches its environment snapshot, which
/// breaks setenv-based tests and CODEX_HOME changes).
enum ProcessEnvironment {
    static func liveValue(_ name: String) -> String? {
        guard let raw = getenv(name) else { return nil }
        let value = String(cString: raw)
        return value.isEmpty ? nil : value
    }
}

public struct ProviderRoot: Equatable, Sendable {
    public let provider: Provider
    /// Expanded absolute path; the directory may not exist yet.
    public let path: String

    public init(provider: Provider, path: String) {
        self.provider = provider
        self.path = path
    }
}

public enum ProviderRoots {
    /// The on-disk session store roots for all 8 supported agents.
    public static func all() -> [ProviderRoot] {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        func under(_ relative: String) -> String { home + "/" + relative }

        let codexSessions: String
        if let codexHome = ProcessEnvironment.liveValue("CODEX_HOME") {
            codexSessions = (codexHome as NSString).expandingTildeInPath + "/sessions"
        } else {
            codexSessions = under(".codex/sessions")
        }

        return [
            ProviderRoot(provider: .antigravity, path: under(".gemini/antigravity-cli/brain")),
            ProviderRoot(provider: .claude, path: under(".claude/projects")),
            ProviderRoot(provider: .codex, path: codexSessions),
            ProviderRoot(provider: .cursor, path: under(".cursor/chats")),
            ProviderRoot(provider: .cursoride, path: under("Library/Application Support/Cursor/User/globalStorage")),
            ProviderRoot(provider: .deepseek, path: under(".deepseek/sessions")),
            ProviderRoot(provider: .droid, path: under(".factory/sessions")),
            ProviderRoot(provider: .gemini, path: under(".gemini/tmp")),
        ]
    }
}
