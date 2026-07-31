# Module contracts

Public API shapes each module must expose, so independently built modules integrate without rework. Implementers may add members but must not rename or reshape what is written here. Language mode Swift 5.9, `SWIFT_STRICT_CONCURRENCY=minimal`, macOS 14, no external dependencies, XCTest for tests. Style mirrors rookery/clients/mac: `final class` services, async throwing methods, closure callbacks, no Combine. No em dashes in user-visible strings.

## SpecStoryKit/Sources/SpecStoryKit

### Cloud + auth (CloudModels.swift, CloudAPI.swift, KeychainStore.swift, AuthManager.swift)

```swift
public struct DeviceMetadata: Codable, Sendable {
    // hostname, os "darwin", osVersion, osDisplayName "macOS", architecture,
    // username, client "specstory-macapp", clientVersion
    public static func current(clientVersion: String) -> DeviceMetadata
}

public enum CloudAPIError: Error {
    case unauthorized            // 401 after retries
    case forbidden               // 403 (upgrade-required states)
    case notFound                // 404
    case http(status: Int, body: String)
    case network(Error)
    case decoding(Error)
}

public actor CloudAPI {
    // accessTokenProvider returns a currently valid access token or nil (signed out).
    public init(baseURL: URL, accessTokenProvider: @escaping () async throws -> String?)

    // Device auth (no bearer header on login):
    public func deviceLogin(code: String, metadata: DeviceMetadata) async throws -> DeviceLoginResult   // refreshToken, expiresAt, email
    public func deviceRefresh(refreshToken: String) async throws -> AccessTokenResult                   // accessToken, expiresAt
    public func deviceLogout(refreshToken: String) async                                                // best effort, never throws

    // Reads (Bearer access token, retry 5xx/429 twice with 300/900 ms backoff):
    public func recentSessions(limit: Int) async throws -> [CloudSession]
    public func projects() async throws -> [CloudProject]
    public func sessions(projectID: String, limit: Int) async throws -> [CloudSession]
    public func sessionMarkdown(projectID: String, sessionID: String, etag: String?) async throws -> SessionMarkdown  // .notModified on 304
    public func sessionHead(projectID: String, sessionID: String) async throws -> SessionHead           // markdownSize, sessionDataSize, etag
    public func entitlement() async throws -> Entitlement                                               // features set; fail closed
    public func flags() async throws -> [String: Bool]                                                  // fail open to [:]
    public func userTools() async throws -> [UserTool]                                                  // agentName, sessionCount, lastUsed
    public func searchSessions(query: String, projectIDs: [String]?, timeFilter: String?, agentNames: [String]?, limit: Int) async throws -> [SearchHit]
    public func updateSession(projectID: String, sessionID: String, userTitle: String?, shareStatus: String?) async throws
}

// CloudSession decodes the /api/v1 envelope shapes; must include: id (server),
// clientId, projectId, name, userTitle?, createdAt, updatedAt, startedAt?,
// endedAt?, sessionDataSize?, metadata (agentName?, deviceId?, machineName?,
// title?, gitBranches?, llmModels?). Envelope is {"success":true,"data":...};
// unwrap generically.

public final class KeychainStore {
    public init(service: String)                      // app uses "com.specstory.mac"
    public func string(for key: String) -> String?
    public func set(_ value: String, for key: String) throws   // upsert
    public func delete(_ key: String)
}

public enum AuthState: Equatable {
    case signedOut
    case signedIn(email: String)
}

// Owns tokens: refresh token in Keychain key "cloud.refresh", cached access
// token + expiry in memory (and Keychain "cloud.access" for warm starts).
// validAccessToken() refreshes when missing/expired/<5 min left, serialized so
// concurrent callers trigger one refresh; 401 on refresh clears state.
public final class AuthManager {
    public init(baseURL: URL, keychain: KeychainStore, clientVersion: String)
    public var api: CloudAPI { get }
    public private(set) var state: AuthState { get }
    public var onStateChange: ((AuthState) -> Void)?
    public func bootstrap()                                    // restore from Keychain at launch
    public func signIn(deviceCode: String) async throws -> String  // returns email
    public func signOut() async
    public func validAccessToken() async throws -> String?
    public var refreshTokenForCLI: String? { get }             // env SPECSTORY_CLOUD_TOKEN for children
}
```

### Chat stream (NDJSONLineBuffer.swift, ChatModels.swift, ChatStreamClient.swift)

```swift
public struct NDJSONLineBuffer {
    public init()
    public mutating func append(_ chunk: some DataProtocol) -> [String]  // complete lines
    public mutating func flush() -> String?                              // trailing partial line at stream end
}

public struct ChatSource: Codable, Equatable, Sendable {
    // chunkID, exchangeID, sessionID, sessionClientID, projectID, sessionName,
    // userTitle?, sessionSummary?, projectName?, projectIcon?, projectColor?
}

public enum ChatStreamEvent: Equatable, Sendable {
    case start(chatSessionID: String)
    case status(String)                 // "chat.stream.event" transient text
    case queryRewritten(String)
    case sources([ChatSource])          // embedding_search_results exchanges
    case chunk(String)                  // answer delta
    case end
    case failure(String)
}

public final class ChatStreamClient {
    public init(baseURL: URL, accessTokenProvider: @escaping () async throws -> String?)
    public func ask(query: String, chatSessionID: String?, projectIDs: [String]?, timeFilter: String?, agentNames: [String]?) -> AsyncThrowingStream<ChatStreamEvent, Error>
    public func chats(limit: Int) async throws -> [ChatThreadSummary]
    public func chat(id: String) async throws -> ChatThread
    public func deleteChat(id: String) async throws
    public func chunk(id: String) async throws -> ChunkDetail    // resolves [chunk:ID] citations
}
```

### Local index (SessionIndexReader.swift, StatsReader.swift)

```swift
public struct IndexedSession: Equatable, Sendable {
    // sessionID, provider (String registry id), projectPath, title/slug,
    // createdAt, updatedAt, userPromptCount?, markdownPath?
}

// Read-only connection to ~/.specstory/sessions.db (SQLite + FTS5, WAL).
// It is a rebuildable cache owned by the CLI: open read-only (SQLITE_OPEN_READONLY),
// tolerate absence (throws .databaseMissing), never write. Schema per
// specstory-cli/docs/SESSIONS-DB.md.
public final class SessionIndexReader {
    public enum ReaderError: Error { case databaseMissing, sqlite(String) }
    public init(databaseURL: URL) throws
    public static var defaultDatabaseURL: URL { get }            // ~/.specstory/sessions.db
    public func recentSessions(limit: Int) throws -> [IndexedSession]
    public func search(_ text: String, limit: Int) throws -> [IndexedSession]  // FTS5 with fallback to LIKE on syntax errors
    public func distinctProjectPaths(limit: Int) throws -> [String]            // most recently active first
}

public enum StatsReader {
    public static func statistics(projectPath: String) -> [String: SessionStats]?   // {outputDir}/statistics.json
    public static func projectInfo(projectPath: String) -> ProjectInfo?             // .specstory/.project.json (workspaceID, gitID?)
}
```

### Engine (BinaryLocator.swift, CLIRunner.swift, WatchSupervisor.swift, SessionTripwire.swift, ProviderRoots.swift)

```swift
public enum BinaryLocator {
    // Order: SPECSTORY_BIN env override (dev/tests), then Bundle.main
    // Resources/bin/specstory_darwin_{arm64|x86_64} by machine arch, verified
    // against Resources/bin/manifest.json sha256 (verification failure throws).
    public static func locate() throws -> URL
}

public enum CLIRunnerEvent: Sendable {
    case watchEvent(WatchEvent)
    case log(level: String, message: String)      // re-leveled slog lines
    case terminated(exitCode: Int32)
}

// One child process. Always appends --no-version-check. Env merges
// SPECSTORY_CLOUD_TOKEN / SPECSTORY_CLOUD_URL when provided. terminate() sends
// SIGTERM and escalates to SIGKILL only after `patience` (default 20 s; caller
// passes longer when uploads may be pending; CLI flush can take 180 s).
public final class CLIRunner {
    public init(binary: URL, arguments: [String], workingDirectory: String, environment: [String: String])
    public var events: AsyncStream<CLIRunnerEvent> { get }
    public func launch() throws
    public func terminate(patience: TimeInterval)
    public var isRunning: Bool { get }

    // One-shot helper: run to completion, return stdout (JSON modes).
    public static func run(binary: URL, arguments: [String], workingDirectory: String, environment: [String: String], timeout: TimeInterval) async throws -> String
}

// Fleet of `watch --json --silent` children, one per project path, capped at
// maxChildren with LRU eviction by last event time. Generation counter so a
// restartAll() during churn keeps only the newest generation. One auto-restart
// after 3 s on unexpected exit.
public final class WatchSupervisor {
    public init(binary: URL, maxChildren: Int, environmentProvider: @escaping () -> [String: String])
    public var onEvent: ((String, WatchEvent) -> Void)?          // (projectPath, event)
    public var onLog: ((String, String, String) -> Void)?        // (projectPath, level, message)
    public private(set) var watchedProjects: [String] { get }
    public func setProjects(_ paths: [String])                   // reconcile fleet to this set (capped)
    public func ensureWatching(_ path: String)                   // tripwire spin-up, may evict LRU
    public func restartAll()                                     // auth/provider/config changes
    public func stopAll(patience: TimeInterval)
}

public struct ProviderRoot: Equatable, Sendable {
    public let provider: Provider
    public let path: String            // expanded absolute path, may not exist yet
}

public enum ProviderRoots {
    public static func all() -> [ProviderRoot]   // the 8 provider store roots (Codex honors $CODEX_HOME)
}

// FSEvents (or DispatchSource directory watch) tripwire over provider roots.
// Detection only: report activity, never parse provider files. projectHint is
// a decoded project path when derivable (claude/droid dir encoding), else nil.
public final class SessionTripwire {
    public init(roots: [ProviderRoot])
    public var onActivity: ((Provider, _ projectHint: String?) -> Void)?
    public func start()
    public func stop()
}
```

## Sources/Services (app target)

```swift
// UNUserNotificationCenter wrapper. Requests authorization lazily on first
// notify call. Category "SESSION_STARTED" carries an "Open" action; the tapped
// session routes through onOpenSession.
final class NotificationService: NSObject, UNUserNotificationCenterDelegate {
    static let shared: NotificationService
    var onOpenSession: ((_ sessionID: String) -> Void)?
    func notifyNewSession(sessionID: String, providerName: String, projectName: String)
    func notifySyncError(message: String)
    func notifySignInNeeded()
}

enum LaunchAtLogin {                    // SMAppService.mainApp
    static var isEnabled: Bool { get }
    static func setEnabled(_ enabled: Bool) throws
}

// Resume lands in a real terminal. Strategy 1: AppleScript into Terminal.app
// (iTerm2 when it is the user's default terminal), cd + command. Strategy 2:
// copy command to pasteboard and open the terminal app. Never place tokens in
// the command string.
enum TerminalLauncher {
    enum LaunchResult { case opened, copiedToPasteboard }
    static func launch(command: String, workingDirectory: String?) -> LaunchResult
    static func copyToPasteboard(command: String)
}
```

## SpecStoryKit: SessionTranscript (transcript parsing for the native viewer)

```swift
/// Parses SpecStory session markdown (v2 grammar, specstory-cli
/// pkg/session/markdown.go) into displayable exchanges.
public struct SessionTranscript: Equatable, Sendable {
    public let exchanges: [Exchange]

    public struct Exchange: Identifiable, Equatable, Sendable {
        public let id: Int                       // orderNumber, 0-based
        public let title: String                 // first 60 chars of first prompt line, markdown-stripped
        public let userSegments: [Segment]       // the user turn(s)
        public let agentSegments: [Segment]      // agent output until next user turn
        public let timestamp: String?            // from the user role header
    }

    public enum Segment: Equatable, Sendable {
        case prose(String)                                     // markdown text
        case code(language: String?, filename: String?, content: String)
        case thinking(String)                                  // <think><details> body
        case toolUse(type: String, name: String, summary: String, body: String)
        case roleHeader(role: String, model: String?, timestamp: String?, sidechain: Bool)
    }

    /// Grammar facts the parser MUST honor:
    /// - Role headers: _**User (TS)**_ / _**Agent (MODEL TS)**_ (also "Assistant"),
    ///   optional " - sidechain" suffix; --- separators only on role transitions.
    /// - File header lines (<!-- Generated by SpecStory ... -->, # <ts>,
    ///   <!-- Provider Session id -->) are skipped.
    /// - <think><details><summary>Thought Process</summary>BODY</details></think> -> .thinking
    /// - <tool-use data-tool-type="T" data-tool-name="N"><details><summary>S</summary>BODY</details></tool-use> -> .toolUse
    /// - Code fences: ```lang or ```lang:filename, closing fence of >= same tick count,
    ///   fences inside tool bodies stay inside the tool segment.
    /// - Consecutive Agent headers with same model dedupe (timestamp ignored).
    /// - An exchange = one user turn plus following agent output.
    public static func parse(markdown: String) -> SessionTranscript
}
```
