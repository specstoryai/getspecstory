import AppKit
import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Resume into a session

    /// Agents the CLI can resume INTO. Antigravity is source-only; Cursor IDE
    /// sessions resume through the Cursor CLI.
    static let resumeTargets: [Provider] = [.claude, .codex, .cursor, .gemini, .deepseek, .droid]

    func resumeTarget(for item: SessionItem) -> Provider {
        guard let provider = Provider(providerID: item.provider) else { return .claude }
        switch provider {
        case .antigravity, .copilotide: return .claude
        case .cursoride: return .cursor
        default: return provider
        }
    }

    /// Whether the Resume affordance shows. Cloud-only sessions with resume
    /// material show it even without entitlement; the tap routes to the
    /// upgrade path (Granola pattern) via requestResume.
    func canResume(_ item: SessionItem) -> Bool {
        if item.origin != .cloudOnly, item.projectPath != nil { return true }
        return (item.sessionDataSize ?? 0) > 0
    }

    /// Whether resume can actually run for this item right now.
    func resumeEntitled(_ item: SessionItem) -> Bool {
        if item.origin != .cloudOnly, item.projectPath != nil { return true }
        return resumeFromCloudAllowed || pro.resumeGate == .enabled
    }

    /// The command shown and executed. Prefers a user-installed CLI so the
    /// terminal command survives app relocation; falls back to the bundled
    /// binary path.
    func resumeCommand(for item: SessionItem, agent: Provider) -> String {
        let cliPath = AppModel.userInstalledCLI() ?? binaryURL?.path ?? "specstory"
        let quotedCLI = cliPath.contains(" ") ? "'\(cliPath)'" : cliPath
        let sessionRef: String
        if let projectID = item.projectID {
            sessionRef = "specstory://projects/\(projectID)/sessions/\(item.clientID)"
        } else {
            sessionRef = item.clientID
        }
        return "\(quotedCLI) resume \(agent.rawValue) --session \(sessionRef)"
    }

    static func userInstalledCLI() -> String? {
        let candidates = [
            "/opt/homebrew/bin/specstory",
            "/usr/local/bin/specstory",
            NSHomeDirectory() + "/.local/bin/specstory",
        ]
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
    }

    /// Cloud resume requires the CLI's own login (tokens never ride argv).
    var cliLoggedIn: Bool {
        FileManager.default.fileExists(atPath: NSHomeDirectory() + "/.specstory/cli/auth.json")
    }

    func requestResume(_ item: SessionItem) {
        guard resumeEntitled(item) else {
            resumeUpsellShown = true
            return
        }
        resumeSheetItem = item
    }

    func performResume(_ item: SessionItem, agent: Provider) {
        let workingDirectory = item.projectPath ?? mappedFolder(for: item)
        guard let workingDirectory else {
            resumeSheetItem = nil
            folderPickItem = item
            return
        }
        let command = resumeCommand(for: item, agent: agent)
        let result = TerminalLauncher.launch(command: command, workingDirectory: workingDirectory)
        resumeSheetItem = nil
        switch result {
        case .opened:
            showToast("Resuming in your terminal")
        case .copiedToPasteboard:
            showToast("Command copied. Paste it into a terminal to resume.")
        }
    }

    // MARK: Cloud-only sessions need a local folder

    private var folderMapping: [String: String] {
        get { UserDefaults.standard.dictionary(forKey: "projectFolders") as? [String: String] ?? [:] }
        set { UserDefaults.standard.set(newValue, forKey: "projectFolders") }
    }

    func mappedFolder(for item: SessionItem) -> String? {
        guard let projectID = item.projectID else { return nil }
        let path = folderMapping[projectID]
        if let path, FileManager.default.fileExists(atPath: path) { return path }
        return nil
    }

    func completeFolderPick(for item: SessionItem, folder: URL) {
        if let projectID = item.projectID {
            var mapping = folderMapping
            mapping[projectID] = folder.path
            folderMapping = mapping
        }
        folderPickItem = nil
        resumeSheetItem = item
    }
}
