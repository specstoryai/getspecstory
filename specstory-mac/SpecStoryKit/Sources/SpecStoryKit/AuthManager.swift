import Foundation

public enum AuthState: Equatable {
    case signedOut
    case signedIn(email: String)
}

/// Owns SpecStory Cloud tokens. The 10 year refresh token lives in the
/// Keychain ("cloud.refresh"); the 1 hour access token is cached in memory
/// and mirrored to the Keychain ("cloud.access") for warm starts. The email
/// is display metadata only and lives in UserDefaults ("cloud.email").
///
/// onStateChange may fire on a background thread; hop to the main actor in UI code.
public final class AuthManager {
    private enum Keys {
        static let refresh = "cloud.refresh"
        static let access = "cloud.access"
        static let email = "cloud.email"
    }

    /// Refresh when the access token has less than this much life left.
    private static let expiryMargin: TimeInterval = 5 * 60
    /// After a network-unreachable refresh failure, do not retry for this long.
    private static let networkCooldown: TimeInterval = 2 * 60

    private struct AccessRecord: Codable {
        let token: String
        let expiresAt: String

        var expiryDate: Date? { CloudDate.parse(expiresAt) }

        var encoded: String {
            guard let data = try? JSONEncoder().encode(self) else { return "" }
            return String(data: data, encoding: .utf8) ?? ""
        }

        init(token: String, expiresAt: String) {
            self.token = token
            self.expiresAt = expiresAt
        }

        init?(encoded: String) {
            guard let data = encoded.data(using: .utf8),
                  let record = try? JSONDecoder().decode(AccessRecord.self, from: data) else { return nil }
            self = record
        }
    }

    // Lets the CloudAPI token-provider closure be created before self is
    // fully initialized without a retain cycle.
    private final class WeakBox {
        weak var manager: AuthManager?
    }

    public let api: CloudAPI
    public var onStateChange: ((AuthState) -> Void)?

    private let keychain: KeychainStore
    private let clientVersion: String
    private let defaults: UserDefaults
    private let lock = NSLock()

    private var stateStorage: AuthState = .signedOut
    private var accessRecord: AccessRecord?
    private var refreshTask: Task<String?, Error>?
    private var cooldownUntil: Date?
    private var lastNetworkError: CloudAPIError?

    public private(set) var state: AuthState {
        get {
            lock.lock()
            defer { lock.unlock() }
            return stateStorage
        }
        set {
            lock.lock()
            stateStorage = newValue
            lock.unlock()
        }
    }

    public convenience init(baseURL: URL, keychain: KeychainStore, clientVersion: String) {
        self.init(baseURL: baseURL, keychain: keychain, clientVersion: clientVersion,
                  configuration: nil, defaults: .standard)
    }

    /// Test hook: inject a stubbed URLSessionConfiguration and an isolated
    /// UserDefaults suite.
    init(baseURL: URL, keychain: KeychainStore, clientVersion: String,
         configuration: URLSessionConfiguration?, defaults: UserDefaults) {
        self.keychain = keychain
        self.clientVersion = clientVersion
        self.defaults = defaults
        let box = WeakBox()
        let provider: () async throws -> String? = {
            try await box.manager?.validAccessToken()
        }
        if let configuration {
            self.api = CloudAPI(baseURL: baseURL, configuration: configuration, accessTokenProvider: provider)
        } else {
            self.api = CloudAPI(baseURL: baseURL, accessTokenProvider: provider)
        }
        box.manager = self
    }

    /// The CLI reuses our refresh token via env SPECSTORY_CLOUD_TOKEN, never argv.
    public var refreshTokenForCLI: String? {
        keychain.string(for: Keys.refresh)
    }

    /// Restores Keychain state at launch. No network calls.
    public func bootstrap() {
        guard keychain.string(for: Keys.refresh) != nil else {
            setState(.signedOut)
            return
        }
        if let raw = keychain.string(for: Keys.access), let record = AccessRecord(encoded: raw) {
            lock.lock()
            accessRecord = record
            lock.unlock()
        }
        let email = defaults.string(forKey: Keys.email) ?? ""
        setState(.signedIn(email: email))
    }

    /// Exchanges a browser-issued device code for a refresh token. Returns the
    /// signed-in email. CloudAPI normalizes the "ABC-123" display form.
    public func signIn(deviceCode: String) async throws -> String {
        let metadata = DeviceMetadata.current(clientVersion: clientVersion)
        let result = try await api.deviceLogin(code: deviceCode, metadata: metadata)
        try keychain.set(result.refreshToken, for: Keys.refresh)
        keychain.delete(Keys.access)
        lock.lock()
        accessRecord = nil
        cooldownUntil = nil
        lastNetworkError = nil
        lock.unlock()
        defaults.set(result.email, forKey: Keys.email)
        setState(.signedIn(email: result.email))
        return result.email
    }

    /// Revokes the refresh token best effort, then clears local state.
    public func signOut() async {
        if let refresh = keychain.string(for: Keys.refresh) {
            await api.deviceLogout(refreshToken: refresh)
        }
        clearLocalState()
    }

    /// Returns a currently valid access token, refreshing when it is missing,
    /// expired, or within 5 minutes of expiry. Concurrent callers share one
    /// in-flight refresh. Returns nil when signed out (including after a 401
    /// on refresh, which clears tokens and transitions to signedOut).
    public func validAccessToken() async throws -> String? {
        let task: Task<String?, Error>
        lock.lock()
        if let record = accessRecord, let expiry = record.expiryDate,
           expiry.timeIntervalSinceNow > Self.expiryMargin {
            lock.unlock()
            return record.token
        }
        if let existing = refreshTask {
            lock.unlock()
            return try await existing.value
        }
        if let until = cooldownUntil, until > Date() {
            let error = lastNetworkError ?? CloudAPIError.network(URLError(.notConnectedToInternet))
            lock.unlock()
            throw error
        }
        lock.unlock()

        // Keychain read happens outside the lock; NSLock is not reentrant and
        // KeychainStore locks internally.
        guard keychain.string(for: Keys.refresh) != nil else {
            return nil
        }

        lock.lock()
        if let existing = refreshTask {
            task = existing
        } else {
            let newTask = Task<String?, Error> { [weak self] in
                try await self?.performRefresh()
            }
            refreshTask = newTask
            task = newTask
        }
        lock.unlock()
        return try await task.value
    }

    // MARK: - Internals

    private func performRefresh() async throws -> String? {
        defer {
            lock.lock()
            refreshTask = nil
            lock.unlock()
        }
        guard let refresh = keychain.string(for: Keys.refresh) else {
            return nil
        }
        do {
            let result = try await api.deviceRefresh(refreshToken: refresh)
            let record = AccessRecord(token: result.accessToken, expiresAt: result.expiresAt)
            lock.lock()
            accessRecord = record
            cooldownUntil = nil
            lastNetworkError = nil
            lock.unlock()
            try? keychain.set(record.encoded, for: Keys.access)
            return result.accessToken
        } catch let error as CloudAPIError {
            switch error {
            case .unauthorized:
                // Revoked or expired refresh token: the only way back is a
                // fresh device login.
                clearLocalState()
                return nil
            case .network:
                lock.lock()
                cooldownUntil = Date().addingTimeInterval(Self.networkCooldown)
                lastNetworkError = error
                lock.unlock()
                throw error
            default:
                throw error
            }
        }
    }

    private func clearLocalState() {
        keychain.delete(Keys.refresh)
        keychain.delete(Keys.access)
        defaults.removeObject(forKey: Keys.email)
        lock.lock()
        accessRecord = nil
        cooldownUntil = nil
        lastNetworkError = nil
        lock.unlock()
        setState(.signedOut)
    }

    private func setState(_ newState: AuthState) {
        lock.lock()
        let changed = stateStorage != newState
        stateStorage = newState
        lock.unlock()
        if changed {
            onStateChange?(newState)
        }
    }
}
