import XCTest
@testable import SpecStoryKit

final class CloudAPITests: XCTestCase {
    private var api: CloudAPI!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        api = CloudAPI(
            baseURL: URL(string: "https://cloud.test")!,
            configuration: StubURLProtocol.makeConfiguration(),
            accessTokenProvider: { "test-access-token" }
        )
    }

    private func metadata() -> DeviceMetadata {
        DeviceMetadata(hostname: "mac.local", os: "darwin", osVersion: "14.0.0",
                       osDisplayName: "macOS", architecture: "arm64", username: "greg",
                       client: "specstory-macapp", clientVersion: "1.0.0")
    }

    // MARK: - Envelope

    func testEnvelopeUnwrapRecentSessions() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"sessions":[
          {"id":"srv-uuid","clientId":"client-1","projectId":"proj-1","name":"a session",
           "userTitle":"Renamed","createdAt":"2026-07-30T10:00:00.000Z","updatedAt":"2026-07-30T11:00:00Z",
           "sessionDataSize":42,"etag":"W/\\"abc\\"","someUnknownField":{"nested":true},
           "metadata":{"agentName":"Claude Code","deviceId":"dev-1","machineName":"mac.local",
                       "title":"first prompt","gitBranches":["main"],"llmModels":["claude-sonnet"],
                       "futureField":123}}
        ]}}
        """)

        let sessions = try await api.recentSessions(limit: 5)
        XCTAssertEqual(sessions.count, 1)
        let session = sessions[0]
        XCTAssertEqual(session.id, "srv-uuid")
        XCTAssertEqual(session.clientId, "client-1")
        XCTAssertEqual(session.projectId, "proj-1")
        XCTAssertEqual(session.userTitle, "Renamed")
        XCTAssertEqual(session.sessionDataSize, 42)
        XCTAssertEqual(session.etag, "W/\"abc\"")
        XCTAssertEqual(session.metadata.agentName, "Claude Code")
        XCTAssertEqual(session.metadata.gitBranches, ["main"])
        XCTAssertNotNil(session.createdAtDate)
        XCTAssertNotNil(session.updatedAtDate)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/sessions/recent")
        XCTAssertTrue(request.url.query?.contains("limit=5") ?? false)
        XCTAssertEqual(request.header("Authorization"), "Bearer test-access-token")
    }

    func testEnvelopeSuccessFalseThrowsHTTPError() async {
        StubURLProtocol.enqueue(json: #"{"success":false,"error":"something broke"}"#)
        do {
            _ = try await api.projects()
            XCTFail("expected error")
        } catch let error as CloudAPIError {
            guard case .http(_, let body) = error else { return XCTFail("wrong error: \(error)") }
            XCTAssertTrue(body.contains("something broke"))
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    // MARK: - Device login

    func testDeviceLoginHappyPathStripsDashAndSendsNoAuthHeader() async throws {
        StubURLProtocol.enqueue(json: """
        {"refreshToken":"refresh-jwt","createdAt":"2026-07-31T00:00:00Z",
         "expiresAt":"2036-07-31T00:00:00Z","user":{"email":"greg@example.com"}}
        """)

        let result = try await api.deviceLogin(code: " Abc-123 ", metadata: metadata())
        XCTAssertEqual(result.refreshToken, "refresh-jwt")
        XCTAssertEqual(result.email, "greg@example.com")
        XCTAssertNotNil(result.expiresAtDate)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/device-login")
        XCTAssertEqual(request.method, "POST")
        XCTAssertNil(request.header("Authorization"))
        let body = try XCTUnwrap(request.bodyJSON)
        XCTAssertEqual(body["device_code"] as? String, "Abc123")
        XCTAssertEqual(body["client"] as? String, "specstory-macapp")
        XCTAssertEqual(body["os"] as? String, "darwin")
        XCTAssertEqual(body["os_display_name"] as? String, "macOS")
        XCTAssertEqual(body["os_version"] as? String, "14.0.0")
        XCTAssertEqual(body["client_version"] as? String, "1.0.0")
    }

    func testDeviceLoginRejectsMalformedCodeWithoutNetworkCall() async {
        do {
            _ = try await api.deviceLogin(code: "AB!", metadata: metadata())
            XCTFail("expected rejection")
        } catch let error as CloudAPIError {
            guard case .deviceCodeRejected(let message) = error else { return XCTFail("wrong error: \(error)") }
            XCTAssertTrue(message.contains("6 character"))
        } catch {
            XCTFail("wrong error type: \(error)")
        }
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testDeviceLoginErrorCopyIsDistinctPerFailure() async throws {
        let cases: [(json: String, expectedFragment: String)] = [
            (#"{"success":false,"error":"Device code has already been used"}"#, "already been used"),
            (#"{"success":false,"error":"Device code expired"}"#, "expired"),
            (#"{"success":false,"error":"Invalid or expired device code"}"#, "not recognized"),
            (#"{"success":false,"error":"Invalid device code"}"#, "not recognized"),
        ]
        for testCase in cases {
            StubURLProtocol.reset()
            StubURLProtocol.enqueue(status: 401, json: testCase.json)
            do {
                _ = try await api.deviceLogin(code: "Abc123", metadata: metadata())
                XCTFail("expected rejection")
            } catch let error as CloudAPIError {
                guard case .deviceCodeRejected(let message) = error else { return XCTFail("wrong error: \(error)") }
                XCTAssertTrue(message.lowercased().contains(testCase.expectedFragment),
                              "expected '\(testCase.expectedFragment)' in '\(message)'")
                XCTAssertFalse(message.contains("\u{2014}"), "no em dashes in user-visible copy")
            }
        }

        StubURLProtocol.reset()
        StubURLProtocol.enqueue(status: 400, json: #"{"success":false,"error":"validation"}"#)
        do {
            _ = try await api.deviceLogin(code: "Abc123", metadata: metadata())
            XCTFail("expected rejection")
        } catch let error as CloudAPIError {
            guard case .deviceCodeRejected(let message) = error else { return XCTFail("wrong error: \(error)") }
            XCTAssertTrue(message.contains("6 character"))
        }
    }

    // MARK: - Refresh and logout

    func testDeviceRefreshSendsBearerRefreshToken() async throws {
        StubURLProtocol.enqueue(json: """
        {"accessToken":"access-jwt","createdAt":"2026-07-31T00:00:00Z","expiresAt":"2026-07-31T01:00:00Z"}
        """)
        let result = try await api.deviceRefresh(refreshToken: "refresh-jwt")
        XCTAssertEqual(result.accessToken, "access-jwt")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/device-refresh")
        XCTAssertEqual(request.header("Authorization"), "Bearer refresh-jwt")
    }

    func testDeviceLogoutNeverThrows() async {
        StubURLProtocol.enqueue(status: 500, json: "boom")
        await api.deviceLogout(refreshToken: "refresh-jwt")
        XCTAssertEqual(StubURLProtocol.requests.count, 1)
        XCTAssertEqual(StubURLProtocol.requests.first?.url.path, "/api/v1/device-logout")
    }

    // MARK: - Markdown + HEAD

    func testSessionMarkdownNeverSendsIfNoneMatch() async throws {
        // The server 500s on a matching If-None-Match and drops the ETag on
        // markdown responses, so conditional refresh is HEAD-driven and the
        // etag parameter must never reach the wire.
        StubURLProtocol.enqueue(json: "# Session")
        let result = try await api.sessionMarkdown(projectID: "proj-1", sessionID: "sess-1", etag: "W/\"abc\"")
        XCTAssertEqual(result, .content(markdown: "# Session", etag: nil))

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/projects/proj-1/sessions/sess-1")
        XCTAssertEqual(request.header("Accept"), "text/markdown")
        XCTAssertNil(request.header("If-None-Match"))
    }

    func testSessionMarkdown200CapturesETagHeader() async throws {
        StubURLProtocol.enqueue(headers: ["ETag": "W/\"v2\"", "Content-Type": "text/markdown"], json: "# Session\n\nHello")
        let result = try await api.sessionMarkdown(projectID: "p", sessionID: "s", etag: nil)
        XCTAssertEqual(result, .content(markdown: "# Session\n\nHello", etag: "W/\"v2\""))
        XCTAssertNil(StubURLProtocol.requests.first?.header("If-None-Match"))
    }

    func testSessionHeadParsesSizeHeaders() async throws {
        StubURLProtocol.enqueue(headers: [
            "ETag": "W/\"h1\"",
            "X-Markdown-Size": "2048",
            "X-Raw-Data-Size": "8192",
            "X-Session-Data-Size": "0",
            "Last-Modified": "Thu, 31 Jul 2026 12:00:00 GMT",
        ])
        let head = try await api.sessionHead(projectID: "p", sessionID: "s")
        XCTAssertEqual(head.etag, "W/\"h1\"")
        XCTAssertEqual(head.markdownSize, 2048)
        XCTAssertEqual(head.rawDataSize, 8192)
        XCTAssertEqual(head.sessionDataSize, 0)
        XCTAssertEqual(StubURLProtocol.requests.first?.method, "HEAD")
    }

    // MARK: - Flags fail open, entitlement fails closed

    func testFlagsFailOpenToEmptyOnHTTPError() async throws {
        StubURLProtocol.enqueue(status: 401)
        let flags = try await api.flags()
        XCTAssertEqual(flags, [:])
    }

    func testFlagsFailOpenToEmptyOnGarbageBody() async throws {
        StubURLProtocol.enqueue(json: "not json at all")
        let flags = try await api.flags()
        XCTAssertEqual(flags, [:])
    }

    func testFlagsDecodeDirectDictionary() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"data":{"skills":true,"analytics":false}}"#)
        let flags = try await api.flags()
        XCTAssertEqual(flags, ["skills": true, "analytics": false])
    }

    func testEntitlementDecodesFeatures() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"data":{"plan":"pro","features":{"resume":true,"skills":false}}}"#)
        let entitlement = try await api.entitlement()
        XCTAssertEqual(entitlement.plan, "pro")
        XCTAssertTrue(entitlement.isEnabled("resume"))
        XCTAssertFalse(entitlement.isEnabled("skills"))
        XCTAssertFalse(entitlement.isEnabled("never-mentioned"), "entitlements fail closed")
    }

    // MARK: - Retry policy

    func testRetryOn500ThenSuccess() async throws {
        StubURLProtocol.enqueue(status: 500, json: "server error")
        StubURLProtocol.enqueue(json: #"{"success":true,"data":{"projects":[{"id":"p1","name":"Proj"}]}}"#)
        let projects = try await api.projects()
        XCTAssertEqual(projects.map(\.id), ["p1"])
        XCTAssertEqual(StubURLProtocol.requests.count, 2, "one retry after the 500")
    }

    func testNoRetryOn404() async {
        StubURLProtocol.enqueue(status: 404)
        do {
            _ = try await api.sessions(projectID: "p", limit: 10)
            XCTFail("expected error")
        } catch let error as CloudAPIError {
            guard case .notFound = error else { return XCTFail("wrong error: \(error)") }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
        XCTAssertEqual(StubURLProtocol.requests.count, 1)
    }

    // MARK: - Error mapping

    func testErrorMapping401And403() async {
        StubURLProtocol.enqueue(status: 401)
        do {
            _ = try await api.userTools()
            XCTFail("expected error")
        } catch let error as CloudAPIError {
            guard case .unauthorized = error else { return XCTFail("wrong error: \(error)") }
        } catch { XCTFail("wrong error type: \(error)") }

        StubURLProtocol.reset()
        StubURLProtocol.enqueue(status: 403)
        do {
            _ = try await api.entitlement()
            XCTFail("expected error")
        } catch let error as CloudAPIError {
            guard case .forbidden = error else { return XCTFail("wrong error: \(error)") }
        } catch { XCTFail("wrong error type: \(error)") }
    }

    func testSignedOutProviderMapsToUnauthorizedWithoutNetwork() async {
        let signedOut = CloudAPI(
            baseURL: URL(string: "https://cloud.test")!,
            configuration: StubURLProtocol.makeConfiguration(),
            accessTokenProvider: { nil }
        )
        do {
            _ = try await signedOut.recentSessions(limit: 1)
            XCTFail("expected error")
        } catch let error as CloudAPIError {
            guard case .unauthorized = error else { return XCTFail("wrong error: \(error)") }
        } catch { XCTFail("wrong error type: \(error)") }
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    // MARK: - Search

    func testSearchSessionsDecodingAndFilters() async throws {
        StubURLProtocol.enqueue(json: """
        {"data":{"searchSessions":{"total":2,"results":[
          {"id":"uuid-1","clientId":"c1","projectId":"proj-a","name":"fix auth bug","userTitle":null,
           "rank":0.87,"matchingExchanges":[{"id":"ex1","content":"we fixed the token refresh","orderNumber":3}],
           "project":{"id":"proj-a","name":"Alpha","icon":"rocket","color":"blue"}},
          {"id":"uuid-2","clientId":"c2","projectId":"proj-b","name":"second","rank":1,
           "matchingExchanges":[],"project":{"id":"proj-b","name":"Beta"}}
        ]}}}
        """)

        let hits = try await api.searchSessions(query: "token refresh", projectIDs: ["proj-a", "proj-b"],
                                                timeFilter: "last30days", agentNames: ["Claude Code"], limit: 10)
        XCTAssertEqual(hits.count, 2)
        XCTAssertEqual(hits[0].id, "uuid-1")
        XCTAssertEqual(hits[0].rank, 0.87)
        XCTAssertEqual(hits[0].matchingExchanges?.first?.content, "we fixed the token refresh")
        XCTAssertEqual(hits[0].project?.name, "Alpha")
        XCTAssertEqual(hits[1].rank, 1.0, "integer ranks decode too")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/graphql")
        XCTAssertEqual(request.method, "POST")
        let body = try XCTUnwrap(request.bodyJSON)
        XCTAssertTrue((body["query"] as? String)?.contains("searchSessions") ?? false)
        let variables = try XCTUnwrap(body["variables"] as? [String: Any])
        XCTAssertEqual(variables["query"] as? String, "token refresh")
        XCTAssertEqual(variables["limit"] as? Int, 10)
        let filters = try XCTUnwrap(variables["filters"] as? [String: Any])
        XCTAssertEqual(filters["projectIds"] as? [String], ["proj-a", "proj-b"])
        XCTAssertEqual(filters["timeFilter"] as? String, "last30days")
        XCTAssertEqual(filters["agentNames"] as? [String], ["Claude Code"])
    }

    func testSearchSessionsOmitsEmptyFilters() async throws {
        StubURLProtocol.enqueue(json: #"{"data":{"searchSessions":{"total":0,"results":[]}}}"#)
        _ = try await api.searchSessions(query: "q", projectIDs: nil, timeFilter: nil, agentNames: nil, limit: 5)
        let body = try XCTUnwrap(StubURLProtocol.requests.first?.bodyJSON)
        let variables = try XCTUnwrap(body["variables"] as? [String: Any])
        XCTAssertNil(variables["filters"])
    }

    // MARK: - PATCH

    func testUpdateSessionSendsOnlyProvidedFields() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"data":{"session":{"id":"x"}}}"#)
        try await api.updateSession(projectID: "p", sessionID: "s", userTitle: "New title", shareStatus: nil)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.method, "PATCH")
        let body = try XCTUnwrap(request.bodyJSON)
        XCTAssertEqual(body["userTitle"] as? String, "New title")
        XCTAssertNil(body["shareStatus"])
    }

    func testUpdateSessionWithNothingToSendIsANoOp() async throws {
        try await api.updateSession(projectID: "p", sessionID: "s", userTitle: nil, shareStatus: nil)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }
}
