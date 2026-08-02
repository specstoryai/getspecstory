import AppKit
import Foundation
import SwiftUI
import SpecStoryKit

/// Drives the expandable agent cockpit under a live session card: what the
/// agent is doing right now (native session insight), the git working state
/// of its checkout, an on-demand diff, and per-project sync pause. One
/// cockpit is open at a time; while open, a 3 second loop refreshes insight
/// and git state, with all inspection work detached off the main actor.
@MainActor
final class CockpitModel: ObservableObject {
    @Published var expandedSessionID: String?
    @Published var insight: LiveSessionInsight?
    @Published var gitState: GitWorkingState?
    @Published var diffText: String?
    @Published var diffFile: String?
    @Published var loading = false
    @Published var diffLoading = false
    @Published var pausedProjects: Set<String> = []

    private var refreshTask: Task<Void, Never>?
    private var activeProjectPath: String?
    private var supervisorPause: ((String) -> Void)?
    private var supervisorResume: ((String) -> Void)?

    /// AppModel wires the WatchSupervisor pause and resume hooks here so
    /// this file never imports or holds the fleet directly.
    func configure(
        supervisorPause: @escaping (String) -> Void,
        supervisorResume: @escaping (String) -> Void
    ) {
        self.supervisorPause = supervisorPause
        self.supervisorResume = supervisorResume
    }

    // MARK: Expand and collapse

    func toggle(session: LiveSession) {
        if expandedSessionID == session.sessionID {
            collapse()
            return
        }
        refreshTask?.cancel()
        expandedSessionID = session.sessionID
        activeProjectPath = session.projectPath
        insight = nil
        gitState = nil
        diffText = nil
        diffFile = nil
        diffLoading = false
        loading = true
        startRefreshLoop(
            provider: session.provider,
            sessionID: session.sessionID,
            projectPath: session.projectPath
        )
    }

    func collapse() {
        refreshTask?.cancel()
        refreshTask = nil
        expandedSessionID = nil
        activeProjectPath = nil
        insight = nil
        gitState = nil
        diffText = nil
        diffFile = nil
        diffLoading = false
        loading = false
    }

    /// The directory cockpit actions operate in: the agent's reported cwd
    /// when known (worktrees differ from the watched project path), else the
    /// project path itself.
    var workingDirectory: String? {
        if let cwd = insight?.cwd, !cwd.isEmpty { return cwd }
        return activeProjectPath
    }

    // MARK: Refresh loop

    private func startRefreshLoop(provider: String, sessionID: String, projectPath: String) {
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                // Inspection reads provider stores and shells out to git:
                // never on the main actor.
                let snapshot = await Task.detached(priority: .utility) { () -> (LiveSessionInsight?, GitWorkingState?) in
                    let insight = NativeSessionInspector.inspect(
                        provider: provider, sessionID: sessionID, projectPath: projectPath)
                    let root = (insight?.cwd).flatMap { $0.isEmpty ? nil : $0 } ?? projectPath
                    let git = GitInspector.state(at: root)
                    return (insight, git)
                }.value
                guard let self, !Task.isCancelled, self.expandedSessionID == sessionID else { return }
                self.insight = snapshot.0
                self.gitState = snapshot.1
                self.loading = false
                try? await Task.sleep(nanoseconds: 3_000_000_000)
            }
        }
    }

    // MARK: Diff

    /// Loads the working diff (nil file means the full diff) off the main
    /// actor. A newer request wins; a stale completion is dropped.
    func showDiff(file: String?) {
        guard let root = workingDirectory else { return }
        diffFile = file
        diffText = nil
        diffLoading = true
        Task { [weak self] in
            let text = await Task.detached(priority: .userInitiated) {
                GitInspector.diff(at: root, file: file)
            }.value
            guard let self, self.expandedSessionID != nil, self.diffFile == file, self.diffLoading else { return }
            self.diffLoading = false
            if let text, !text.isEmpty {
                self.diffText = text
            } else {
                self.diffText = "No changes to show."
            }
        }
    }

    func closeDiff() {
        diffFile = nil
        diffText = nil
        diffLoading = false
    }

    // MARK: Sync pause

    func pauseSync(projectPath: String) {
        pausedProjects.insert(projectPath)
        supervisorPause?(projectPath)
    }

    func resumeSync(projectPath: String) {
        pausedProjects.remove(projectPath)
        supervisorResume?(projectPath)
    }

    func isPaused(_ projectPath: String) -> Bool {
        pausedProjects.contains(projectPath)
    }

    // MARK: Terminal

    /// Opens the user's terminal already sitting in the given directory.
    /// Choice documented: this reuses TerminalLauncher rather than opening
    /// the folder via NSWorkspace because TerminalLauncher already prefers
    /// iTerm2 when it is the user's terminal and degrades to pasteboard plus
    /// app-open on a TCC Automation denial. ":" is the POSIX no-op builtin,
    /// so the launched line is just "cd '<dir>' && :" and the window lands
    /// in the directory with nothing else run.
    func openTerminal(at path: String) {
        _ = TerminalLauncher.launch(command: ":", workingDirectory: path)
    }
}
