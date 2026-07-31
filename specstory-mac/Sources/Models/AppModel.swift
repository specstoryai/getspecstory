import AppKit
import Foundation
import SwiftUI
import SpecStoryKit

/// Navigation destinations for the desktop window sidebar.
enum PanelMode: String, CaseIterable, Identifiable {
    case home
    case chat
    case providers
    case settings

    var id: String { rawValue }
}

/// Menu bar icon states.
enum MenuBarState {
    case idle
    case live
    case syncing
    case error
    case signedOut
}

/// The app's single source of truth (Rook's god-model pattern): all mutation
/// happens on the main actor; services stay dumb and report back through
/// closures, which hop here before touching state.
@MainActor
final class AppModel: ObservableObject {
    static private(set) weak var shared: AppModel?

    // MARK: Services

    static let cloudBaseURL = URL(string: ProcessInfo.processInfo.environment["SPECSTORY_CLOUD_URL"] ?? "https://cloud.specstory.com")!
    static var cloudURLOverride: String? { ProcessInfo.processInfo.environment["SPECSTORY_CLOUD_URL"] }

    let keychain: KeychainStore
    let auth: AuthManager
    let chatClient: ChatStreamClient
    var indexReader: SessionIndexReader?
    var supervisor: WatchSupervisor?
    var tripwire: SessionTripwire?
    private(set) var binaryURL: URL?

    // MARK: Published state

    @Published var panelMode: PanelMode = .home
    @Published var authState: AuthState = .signedOut
    @Published var bootError: String?
    @Published var cloudError: String?
    @Published var toast: String?

    @Published var allItems: [SessionItem] = []
    @Published var feedSections: [FeedSection] = []
    @Published var liveSessions: [String: LiveSession] = [:]
    @Published var cloudProjects: [CloudProject] = []

    @Published var searchQuery = ""
    @Published var searchResults: [SessionItem] = []
    @Published var searchOverlayShown = false

    @Published var selectedSession: SessionItem?
    @Published var sessionMarkdown: String?
    @Published var detailLoading = false

    @Published var askQuery = ""
    @Published var askMessages: [AskMessage] = []
    @Published var askStreaming = false

    @Published var providerStatuses: [ProviderStatus] = []
    @Published var resumeSheetItem: SessionItem?
    @Published var folderPickItem: SessionItem?

    // MARK: Settings (UserDefaults-backed)

    @Published var notificationsEnabled: Bool {
        didSet { UserDefaults.standard.set(notificationsEnabled, forKey: "notificationsEnabled") }
    }
    @Published var cloudSyncEnabled: Bool {
        didSet {
            UserDefaults.standard.set(cloudSyncEnabled, forKey: "cloudSyncEnabled")
            supervisor?.restartAll()
            Task { await refreshFeed() }
        }
    }
    @Published var launchAtLoginEnabled: Bool {
        didSet {
            do { try LaunchAtLogin.setEnabled(launchAtLoginEnabled) } catch {
                toast = "Could not update login item"
                launchAtLoginEnabled = LaunchAtLogin.isEnabled
            }
        }
    }

    // MARK: Internal work state

    var chatSessionID: String?
    var markdownCache: [String: (etag: String?, markdown: String)] = [:]
    var searchTask: Task<Void, Never>?
    var askTask: Task<Void, Never>?
    var feedRefreshTask: Task<Void, Never>?
    var livePruneTimer: Timer?
    var currentEntitlement: Entitlement?
    var currentFlags: [String: Bool] = [:]
    private var booted = false

    init() {
        keychain = KeychainStore(service: "com.specstory.mac")
        let version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.0.0"
        auth = AuthManager(baseURL: AppModel.cloudBaseURL, keychain: keychain, clientVersion: version)
        chatClient = ChatStreamClient(
            baseURL: AppModel.cloudBaseURL,
            accessTokenProvider: { [auth] in try await auth.validAccessToken() }
        )
        notificationsEnabled = UserDefaults.standard.object(forKey: "notificationsEnabled") as? Bool ?? true
        cloudSyncEnabled = UserDefaults.standard.object(forKey: "cloudSyncEnabled") as? Bool ?? true
        launchAtLoginEnabled = LaunchAtLogin.isEnabled
        AppModel.shared = self

        auth.onStateChange = { [weak self] state in
            Task { @MainActor in self?.handleAuthChange(state) }
        }
        NotificationService.shared.onOpenSession = { [weak self] sessionID in
            Task { @MainActor in
                self?.revealSession(id: sessionID)
                (NSApp.delegate as? AppDelegate)?.showMainWindow()
            }
        }
    }

    // MARK: Boot

    func bootstrap() async {
        guard !booted else { return }
        booted = true

        auth.bootstrap()
        authState = auth.state

        do {
            binaryURL = try BinaryLocator.locate()
        } catch {
            bootError = "The bundled specstory binary is unavailable: \(error.localizedDescription)"
        }

        // Show whatever the existing index has immediately; freshen behind it.
        openIndexReader()
        await refreshFeed()
        startWatchers()
        startLivePruneTimer()
        Task { await refreshGates() }
        Task { await refreshProviderStatuses() }

        if let binaryURL {
            // Rebuild the cross-project index in the background so the feed
            // becomes complete; watch children keep it live afterwards.
            Task { [weak self] in
                _ = try? await CLIRunner.run(
                    binary: binaryURL, arguments: ["reindex", "--silent"],
                    workingDirectory: NSHomeDirectory(), environment: [:], timeout: 600
                )
                guard let self else { return }
                self.openIndexReader()
                await self.refreshFeed()
                self.supervisor?.setProjects(self.recentProjectPaths())
            }
        }
    }

    func openIndexReader() {
        indexReader = try? SessionIndexReader(databaseURL: SessionIndexReader.defaultDatabaseURL)
    }

    /// Feature gates: flags fail open, entitlements fail closed.
    func refreshGates() async {
        guard case .signedIn = authState else {
            currentEntitlement = nil
            currentFlags = [:]
            return
        }
        currentFlags = (try? await auth.api.flags()) ?? [:]
        currentEntitlement = try? await auth.api.entitlement()
    }

    var resumeFromCloudAllowed: Bool {
        guard let entitlement = currentEntitlement else { return false }
        return entitlement.isEnabled("resume") && (currentFlags["resume"] ?? true)
    }

    // MARK: Menu bar

    var menuBarState: MenuBarState {
        if bootError != nil { return .error }
        if !liveSessions.isEmpty { return .live }
        if case .signedOut = authState { return .signedOut }
        return .idle
    }

    var statusText: String {
        if bootError != nil { return "Needs attention" }
        let liveCount = liveSessions.count
        if liveCount > 0 {
            return "\(liveCount) live session\(liveCount == 1 ? "" : "s")"
        }
        if case .signedOut = authState { return "Signed out" }
        return "Watching for sessions"
    }

    var menuBarHelp: String { "SpecStory: \(statusText)" }

    func showToast(_ message: String) {
        toast = message
        Task {
            try? await Task.sleep(nanoseconds: 3_000_000_000)
            if toast == message { toast = nil }
        }
    }

    func quitApp() {
        supervisor?.stopAll(patience: 20)
        tripwire?.stop()
        NSApplication.shared.terminate(nil)
    }
}
