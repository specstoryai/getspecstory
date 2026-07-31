import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Watch fleet + tripwire

    func startWatchers() async {
        guard let binaryURL else { return }
        reapOrphanChildren(binary: binaryURL)

        // Captured directly so the closure never touches main-actor state
        // from the supervisor queue (AuthManager and UserDefaults are safe
        // off-main).
        let auth = self.auth
        let supervisor = WatchSupervisor(binary: binaryURL, maxChildren: 8, environmentProvider: {
            var env = [String: String]()
            let syncOn = UserDefaults.standard.object(forKey: "cloudSyncEnabled") as? Bool ?? true
            if syncOn, let token = auth.refreshTokenForCLI {
                env["SPECSTORY_CLOUD_TOKEN"] = token
            }
            if let override = AppModel.cloudURLOverride {
                env["SPECSTORY_CLOUD_URL"] = override
            }
            return env
        })
        supervisor.onEvent = { [weak self] projectPath, event in
            Task { @MainActor in self?.handleWatchEvent(projectPath: projectPath, event: event) }
        }
        self.supervisor = supervisor
        supervisor.setProjects(await recentProjectPaths())

        let tripwire = SessionTripwire(roots: ProviderRoots.all())
        tripwire.onActivity = { [weak self] provider, projectHint in
            Task { @MainActor in self?.handleTripwire(provider: provider, projectHint: projectHint) }
        }
        self.tripwire = tripwire
        tripwire.start()
    }

    /// CLI children survive an app crash or force-quit (no parent-death
    /// signal on macOS). Anything running our bundled binary before our own
    /// fleet starts is an orphan from a previous run: terminate it.
    private func reapOrphanChildren(binary: URL) {
        let pgrep = Process()
        pgrep.executableURL = URL(fileURLWithPath: "/usr/bin/pgrep")
        pgrep.arguments = ["-f", binary.path]
        let pipe = Pipe()
        pgrep.standardOutput = pipe
        do {
            try pgrep.run()
            pgrep.waitUntilExit()
        } catch {
            return
        }
        let output = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        for pidString in output.split(separator: "\n") {
            if let pid = Int32(pidString.trimmingCharacters(in: .whitespaces)) {
                kill(pid, SIGTERM)
            }
        }
    }

    func recentProjectPaths(limit: Int = 8) async -> [String] {
        let paths = await indexBox.distinctProjectPaths(limit: limit * 2)
        return Array(paths.filter { FileManager.default.fileExists(atPath: $0) }.prefix(limit))
    }

    func handleWatchEvent(projectPath: String, event: WatchEvent) {
        let isNew = liveSessions[event.sessionID] == nil
        if var live = liveSessions[event.sessionID] {
            live.apply(event)
            liveSessions[event.sessionID] = live
        } else {
            liveSessions[event.sessionID] = LiveSession(projectPath: projectPath, event: event)
        }

        if event.action == .created || isNew {
            notifyNewSessionIfEnabled(event: event, projectPath: projectPath)
        }
        scheduleFeedRefresh()
    }

    private func notifyNewSessionIfEnabled(event: WatchEvent, projectPath: String) {
        guard notificationsEnabled, event.action == .created else { return }
        let provider = Provider(providerID: event.provider)
        if let provider, !providerNotificationsOn(provider) { return }
        let providerName = provider?.displayName ?? event.provider.capitalized
        let projectName = SessionItem.folderName(of: projectPath) ?? "a project"
        NotificationService.shared.notifyNewSession(
            sessionID: event.sessionID,
            providerName: providerName,
            projectName: projectName
        )
    }

    func handleTripwire(provider: Provider, projectHint: String?) {
        if let hint = projectHint, FileManager.default.fileExists(atPath: hint) {
            supervisor?.ensureWatching(hint)
        }
    }

    func startLivePruneTimer() {
        livePruneTimer?.invalidate()
        livePruneTimer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.pruneLiveSessions() }
        }
    }

    /// A session stops being "live" after 10 minutes without events.
    func pruneLiveSessions() {
        let cutoff = Date().addingTimeInterval(-600)
        let stale = liveSessions.values.filter { $0.lastEventAt < cutoff }.map(\.sessionID)
        guard !stale.isEmpty else { return }
        for id in stale {
            liveSessions.removeValue(forKey: id)
        }
        scheduleFeedRefresh(after: 1)
    }

    /// Live sessions newest-first for the pinned section.
    var liveSessionsOrdered: [LiveSession] {
        liveSessions.values.sorted { $0.lastEventAt > $1.lastEventAt }
    }
}
