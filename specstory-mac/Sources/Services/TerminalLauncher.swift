import AppKit
import Foundation

/// Lands a resume command in a real terminal. Strategy 1 is AppleScript into
/// the user's terminal (iTerm2 when it is running or is the default handler,
/// else Terminal.app), which needs the Automation TCC grant. When that fails,
/// strategy 2 copies the command to the pasteboard and opens the terminal app
/// so the user can paste it themselves. Callers must never place tokens in the
/// command string; it ends up in AppleScript source and on the pasteboard.
enum TerminalLauncher {
    enum LaunchResult {
        case opened
        case copiedToPasteboard
    }

    private static let iTermBundleID = "com.googlecode.iterm2"
    private static let terminalBundleID = "com.apple.Terminal"

    static func launch(command: String, workingDirectory: String?) -> LaunchResult {
        let shellLine = shellLine(command: command, workingDirectory: workingDirectory)
        let useITerm = iTermIsPreferred()
        let source = useITerm ? iTermScript(running: shellLine) : terminalScript(running: shellLine)

        var errorInfo: NSDictionary?
        if let script = NSAppleScript(source: source) {
            script.executeAndReturnError(&errorInfo)
            if errorInfo == nil {
                return .opened
            }
        }

        // AppleScript failed (commonly a TCC Automation denial): fall back to
        // pasteboard plus opening the terminal app.
        copyToPasteboard(command: shellLine)
        openTerminalApp(bundleID: useITerm ? iTermBundleID : terminalBundleID)
        return .copiedToPasteboard
    }

    static func copyToPasteboard(command: String) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(command, forType: .string)
    }

    // MARK: - Shell line

    private static func shellLine(command: String, workingDirectory: String?) -> String {
        guard let dir = workingDirectory, !dir.isEmpty else { return command }
        return "cd '\(singleQuoteEscaped(dir))' && \(command)"
    }

    /// POSIX single-quote escaping: close the quote, emit an escaped quote,
    /// reopen the quote.
    private static func singleQuoteEscaped(_ text: String) -> String {
        text.replacingOccurrences(of: "'", with: "'\\''")
    }

    // MARK: - AppleScript

    private static func terminalScript(running shellLine: String) -> String {
        let quoted = appleScriptStringLiteral(shellLine)
        return """
        tell application "Terminal"
            activate
            do script \(quoted)
        end tell
        """
    }

    private static func iTermScript(running shellLine: String) -> String {
        let quoted = appleScriptStringLiteral(shellLine)
        return """
        tell application "iTerm2"
            activate
            set newWindow to (create window with default profile)
            tell current session of newWindow
                write text \(quoted)
            end tell
        end tell
        """
    }

    private static func appleScriptStringLiteral(_ text: String) -> String {
        let escaped = text
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        return "\"\(escaped)\""
    }

    // MARK: - Terminal app selection

    private static func iTermIsPreferred() -> Bool {
        let running = NSWorkspace.shared.runningApplications.contains {
            $0.bundleIdentifier == iTermBundleID
        }
        if running {
            return true
        }
        // iTerm2 registers as the ssh scheme handler when set as default terminal.
        if let sshURL = URL(string: "ssh://localhost"),
           let handler = NSWorkspace.shared.urlForApplication(toOpen: sshURL),
           Bundle(url: handler)?.bundleIdentifier == iTermBundleID {
            return true
        }
        return false
    }

    private static func openTerminalApp(bundleID: String) {
        guard let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
            ?? NSWorkspace.shared.urlForApplication(withBundleIdentifier: terminalBundleID)
        else {
            return
        }
        NSWorkspace.shared.openApplication(
            at: appURL,
            configuration: NSWorkspace.OpenConfiguration(),
            completionHandler: nil
        )
    }
}
