import XCTest
@testable import SpecStoryKit

final class AuthManagerTests: XCTestCase {
    private var keychain: KeychainStore!
    private var defaults: UserDefaults!
    private var suiteName: String!
    private var manager: AuthManager!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        keychain = KeychainStore.ephemeral()
        suiteName = "specstory-auth-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
        manager = AuthManager(
            baseURL: URL(string: "https://cloud.test")!,
            keychain: keychain,
            clientVersion: "1.0.0",
            configuration: StubURLProtocol.makeConfiguration(),
            defaults: defaults
        )
    }

    override func tearDown() {
        defaults.removePersistentDomain(forName: suiteName)
        super.tearDown()
    }

    private func iso(_ interval: TimeInterval) -> String {
        ISO8601DateFormatter().string(from: Date().addingTimeInterval(interval))
    }

    private func seedRefreshToken(_ token: String = "refresh-jwt", email: String = "greg@example.com") {
        try? keychain.set(token, for: "cloud.refresh")
        defaults.set(email, forKey: "cloud.email")
    }

    private func seedAccessToken(_ token: String, expiresIn: TimeInterval) {
        let record = #"{"token":"\#(token)","expiresAt":"\#(iso(expiresIn))"}"#
        try? keychain.set(record, for: "cloud.access")
    }

    private func enqueueRefreshSuccess(token: String = "fresh-access", expiresIn: TimeInterval = 3600, delay: TimeInterval = 0) {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"accessToken":"\(token)","createdAt":"\(iso(0))","expiresAt":"\(iso(expiresIn))"}}
        """, delay: delay)
    }

    // MARK: - Bootstrap

    func testBootstrapRestoresSignedInStateWithoutNetwork() {
        seedRefreshToken(email: "greg@example.com")
        var observed: [AuthState] = []
        manager.onStateChange = { observed.append($0) }

        manager.bootstrap()

        XCTAssertEqual(manager.state, .signedIn(email: "greg@example.com"))
        XCTAssertEqual(observed, [.signedIn(email: "greg@example.com")])
        XCTAssertTrue(StubURLProtocol.requests.isEmpty, "bootstrap must not touch the network")
        XCTAssertEqual(manager.refreshTokenForCLI, "refresh-jwt")
    }

    func testBootstrapWithEmptyKeychainStaysSignedOut() {
        manager.bootstrap()
        XCTAssertEqual(manager.state, .signedOut)
        XCTAssertNil(manager.refreshTokenForCLI)
    }

    // MARK: - validAccessToken

    func testValidAccessTokenReturnsNilWhenSignedOut() async throws {
        let token = try await manager.validAccessToken()
        XCTAssertNil(token)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testValidCachedTokenIsReturnedWithoutRefresh() async throws {
        seedRefreshToken()
        seedAccessToken("cached-access", expiresIn: 3600)
        manager.bootstrap()

        let token = try await manager.validAccessToken()
        XCTAssertEqual(token, "cached-access")
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testRefreshesWhenTokenIsNearExpiry() async throws {
        seedRefreshToken()
        seedAccessToken("stale-access", expiresIn: 120)  // under the 5 minute margin
        manager.bootstrap()
        enqueueRefreshSuccess(token: "fresh-access")

        let token = try await manager.validAccessToken()
        XCTAssertEqual(token, "fresh-access")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/device-refresh")
        XCTAssertEqual(request.header("Authorization"), "Bearer refresh-jwt")

        let stored = try XCTUnwrap(keychain.string(for: "cloud.access"))
        XCTAssertTrue(stored.contains("fresh-access"), "access token mirrored to Keychain for warm starts")

        // The fresh token now serves from memory without another refresh.
        let again = try await manager.validAccessToken()
        XCTAssertEqual(again, "fresh-access")
        XCTAssertEqual(StubURLProtocol.requests.count, 1)
    }

    func testConcurrentCallersShareOneRefresh() async throws {
        seedRefreshToken()
        manager.bootstrap()
        enqueueRefreshSuccess(token: "shared-access", delay: 0.2)

        async let first = manager.validAccessToken()
        async let second = manager.validAccessToken()
        let (a, b) = try await (first, second)

        XCTAssertEqual(a, "shared-access")
        XCTAssertEqual(b, "shared-access")
        XCTAssertEqual(StubURLProtocol.requests.count, 1, "concurrent callers must await a single refresh")
    }

    func testRefresh401ClearsTokensAndSignsOut() async throws {
        seedRefreshToken()
        manager.bootstrap()
        var observed: [AuthState] = []
        manager.onStateChange = { observed.append($0) }
        StubURLProtocol.enqueue(status: 401, json: #"{"success":false,"error":"Refresh token has been revoked"}"#)

        let token = try await manager.validAccessToken()

        XCTAssertNil(token)
        XCTAssertEqual(manager.state, .signedOut)
        XCTAssertEqual(observed, [.signedOut])
        XCTAssertNil(keychain.string(for: "cloud.refresh"))
        XCTAssertNil(keychain.string(for: "cloud.access"))
        XCTAssertNil(defaults.string(forKey: "cloud.email"))
    }

    func testNetworkFailureStartsCooldownAndSkipsImmediateRetry() async {
        seedRefreshToken()
        manager.bootstrap()
        StubURLProtocol.enqueueError(URLError(.notConnectedToInternet))

        do {
            _ = try await manager.validAccessToken()
            XCTFail("expected network error")
        } catch let error as CloudAPIError {
            guard case .network = error else { return XCTFail("wrong error: \(error)") }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
        XCTAssertEqual(StubURLProtocol.requests.count, 1)

        // Within the 2 minute cooldown the second call fails fast, no request.
        do {
            _ = try await manager.validAccessToken()
            XCTFail("expected cooldown error")
        } catch let error as CloudAPIError {
            guard case .network = error else { return XCTFail("wrong error: \(error)") }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
        XCTAssertEqual(StubURLProtocol.requests.count, 1, "cooldown must prevent a second network attempt")

        XCTAssertEqual(manager.state, .signedIn(email: "greg@example.com"),
                       "network failures must not sign the user out")
    }

    // MARK: - Sign in / out

    func testSignInStoresTokensAndTransitionsState() async throws {
        var observed: [AuthState] = []
        manager.onStateChange = { observed.append($0) }
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"refreshToken":"new-refresh","createdAt":"\(iso(0))",
         "expiresAt":"\(iso(315_360_000))","user":{"email":"greg@example.com"}}}
        """)

        let email = try await manager.signIn(deviceCode: "Abc-123")

        XCTAssertEqual(email, "greg@example.com")
        XCTAssertEqual(manager.state, .signedIn(email: "greg@example.com"))
        XCTAssertEqual(observed, [.signedIn(email: "greg@example.com")])
        XCTAssertEqual(keychain.string(for: "cloud.refresh"), "new-refresh")
        XCTAssertEqual(manager.refreshTokenForCLI, "new-refresh")
        XCTAssertEqual(defaults.string(forKey: "cloud.email"), "greg@example.com")

        let body = try XCTUnwrap(StubURLProtocol.requests.first?.bodyJSON)
        XCTAssertEqual(body["device_code"] as? String, "Abc123", "display dash is stripped before sending")
        XCTAssertEqual(body["client"] as? String, "specstory-macapp")
    }

    func testSignOutRevokesBestEffortAndClearsState() async {
        seedRefreshToken()
        seedAccessToken("cached", expiresIn: 3600)
        manager.bootstrap()
        var observed: [AuthState] = []
        manager.onStateChange = { observed.append($0) }
        StubURLProtocol.enqueue(status: 500, json: "logout exploded")  // must still sign out

        await manager.signOut()

        XCTAssertEqual(manager.state, .signedOut)
        XCTAssertEqual(observed, [.signedOut])
        XCTAssertNil(keychain.string(for: "cloud.refresh"))
        XCTAssertNil(keychain.string(for: "cloud.access"))
        XCTAssertEqual(StubURLProtocol.requests.first?.url.path, "/api/v1/device-logout")
        XCTAssertEqual(StubURLProtocol.requests.first?.header("Authorization"), "Bearer refresh-jwt")
    }
}
