import XCTest
@testable import SpecStoryKit

final class ProAPITests: XCTestCase {
    private var api: CloudProAPI!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        api = CloudProAPI(
            baseURL: URL(string: "https://cloud.test")!,
            configuration: StubURLProtocol.makeConfiguration(),
            accessTokenProvider: { "test-access-token" }
        )
    }

    // MARK: - Gate combinator

    func testGateTruthTable() {
        // Flags fail open: a nil flag counts as on.
        XCTAssertEqual(FeatureGate.gate(flagOn: nil, entitled: true), .enabled)
        XCTAssertEqual(FeatureGate.gate(flagOn: nil, entitled: false), .upsell)
        XCTAssertEqual(FeatureGate.gate(flagOn: true, entitled: true), .enabled)
        XCTAssertEqual(FeatureGate.gate(flagOn: true, entitled: false), .upsell)
        // The flag sits above the entitlement: off means hidden regardless.
        XCTAssertEqual(FeatureGate.gate(flagOn: false, entitled: true), .hidden)
        XCTAssertEqual(FeatureGate.gate(flagOn: false, entitled: false), .hidden)
    }

    func testPlanParsingFailsClosedToFree() {
        XCTAssertEqual(Plan(planString: "pro"), .pro)
        XCTAssertEqual(Plan(planString: "TEAM"), .team)
        XCTAssertEqual(Plan(planString: "enterprise"), .enterprise)
        XCTAssertEqual(Plan(planString: "founder"), .free)
        XCTAssertEqual(Plan(planString: nil), .free)
        XCTAssertEqual(Plan.pro.displayName, "Pro")
        XCTAssertLessThan(Plan.free.rank, Plan.pro.rank)
    }

    // MARK: - Skills library

    func testSkillsLibraryDecodesTopLevelShape() async throws {
        // The library endpoint answers {success, skills}, not the data envelope.
        StubURLProtocol.enqueue(json: """
        {"success":true,"skills":[
          {"name":"fix-flaky-tests","state":"ready","trigger":"When tests fail intermittently",
           "confidence":0.87,"verdict":"confirmed","skillMd":"# Fix flaky tests\\n\\nQuarantine first.",
           "clusterKey":"ck1","fingerprint":"fp1","contentSha":"abc123",
           "recommendation":"update","dossierId":"d-1","evidenceRefs":["s1","s2"],
           "createdAt":"2026-07-01T10:00:00Z","kind":"theme","themeLift":1.4,
           "installedAgents":["claude","codex"]},
          {"name":"minimal-skill"}
        ]}
        """)

        let skills = try await api.skillsLibrary()
        XCTAssertEqual(skills.count, 2)

        let first = skills[0]
        XCTAssertEqual(first.name, "fix-flaky-tests")
        XCTAssertEqual(first.state, "ready")
        XCTAssertEqual(first.description, "When tests fail intermittently")
        XCTAssertEqual(first.content, "# Fix flaky tests\n\nQuarantine first.")
        XCTAssertEqual(first.confidence, 0.87)
        XCTAssertEqual(first.verdict, "confirmed")
        XCTAssertEqual(first.recommendation, "update")
        XCTAssertEqual(first.dossierId, "d-1")
        XCTAssertEqual(first.installedAgents, ["claude", "codex"])
        XCTAssertNotNil(first.updatedAtDate)

        // Tolerant decode: absent fields default rather than failing the row.
        let minimal = skills[1]
        XCTAssertEqual(minimal.name, "minimal-skill")
        XCTAssertNil(minimal.description)
        XCTAssertEqual(minimal.content, "")
        XCTAssertNil(minimal.installedAgents)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/skills/library")
        XCTAssertEqual(request.header("Authorization"), "Bearer test-access-token")
    }

    func testSkillsLibraryToleratesEnvelopedVariant() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"skills":[{"name":"enveloped","skillMd":"body"}]}}
        """)
        let skills = try await api.skillsLibrary()
        XCTAssertEqual(skills.map(\.name), ["enveloped"])
        XCTAssertEqual(skills[0].content, "body")
    }

    func testSetSkillInstallStatePatchesInstallState() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true}"#)
        try await api.setSkillInstallState(name: "fix-flaky-tests", installed: true)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/skills/fix-flaky-tests")
        XCTAssertEqual(request.method, "PATCH")
        XCTAssertEqual(request.bodyJSON?["install_state"] as? String, "installed")
    }

    // MARK: - Gate degrade states

    func testForbiddenMapsToUpgradeRequiredWithFeature() async {
        StubURLProtocol.enqueue(status: 403, json: #"{"success":false,"error":"upgrade_required","data":{"feature":"skills"}}"#)
        do {
            _ = try await api.skillsLibrary()
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .upgradeRequired(let feature) = error else {
                return XCTFail("wrong error: \(error)")
            }
            XCTAssertEqual(feature, "skills")
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    func testForbiddenWithoutBodyStillMapsToUpgradeRequired() async {
        StubURLProtocol.enqueue(status: 403, json: "")
        do {
            _ = try await api.resumableProjects()
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .upgradeRequired(let feature) = error else {
                return XCTFail("wrong error: \(error)")
            }
            XCTAssertNil(feature)
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    func testNotFoundMapsToFeatureDark() async {
        // withFlag returns 404 when the environment flag is off: the feature
        // "does not exist" in that environment.
        StubURLProtocol.enqueue(status: 404, json: #"{"success":false,"error":"not found"}"#)
        do {
            _ = try await api.skillsLibrary()
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .featureDark = error else {
                return XCTFail("wrong error: \(error)")
            }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    // MARK: - Resume discovery

    func testResumableProjectsSendsQueryAndDecodesInlineSessions() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"projects":[
          {"id":"proj-1","name":"getspecstory","icon":"rocket","color":"#00AACC",
           "lastUpdated":"2026-07-30T09:00:00Z","sessionCount":2,
           "sessions":[
             {"id":"srv-1","clientId":"client-1","projectId":"proj-1","projectName":"getspecstory",
              "name":"fix auth race","userTitle":"Auth race fix","markdownSize":900,
              "sessionDataSize":4200,"createdAt":"2026-07-29T10:00:00Z","updatedAt":"2026-07-30T09:00:00Z",
              "metadata":{"agentName":"Claude Code","machineName":"studio.local"},"etag":"W/\\"e1\\""}
           ]}
        ],"total":1}}
        """)

        let projects = try await api.resumableProjects()
        XCTAssertEqual(projects.count, 1)
        XCTAssertEqual(projects[0].id, "proj-1")
        XCTAssertEqual(projects[0].sessions.count, 1)
        let session = projects[0].sessions[0]
        XCTAssertEqual(session.clientId, "client-1")
        XCTAssertEqual(session.sessionDataSize, 4200)
        XCTAssertEqual(session.displayTitle, "Auth race fix")
        XCTAssertEqual(session.metadata.machineName, "studio.local")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/projects")
        XCTAssertEqual(request.url.query, "resumable=true")
    }

    func testResumableSessionsForProject() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"sessions":[
          {"id":"srv-2","clientId":"client-2","projectId":"proj-1","name":"port views",
           "sessionDataSize":100,"createdAt":"2026-07-28T08:00:00Z","updatedAt":"2026-07-28T09:00:00Z"}
        ],"total":1,"projectId":"proj-1"}}
        """)

        let sessions = try await api.resumableSessions(projectID: "proj-1")
        XCTAssertEqual(sessions.map(\.clientId), ["client-2"])

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/projects/proj-1/sessions")
        XCTAssertEqual(request.url.query, "resumable=true")
    }

    // MARK: - Resume search snippets

    func testSearchResumeStripsSnippetDelimitersIntoRanges() async throws {
        // STX () opens a highlight, ETX () closes it.
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"results":[
          {"id":"srv-3","clientId":"client-3","projectId":"proj-1","projectName":"getspecstory",
           "name":"auth work","updatedAt":"2026-07-30T11:00:00Z","createdAt":"2026-07-30T10:00:00Z",
           "sessionDataSize":10,"metadata":{"agentName":"Claude Code"},
           "snippet":"fixed the \\u0002auth race\\u0003 in worker"}
        ]}}
        """)

        let hits = try await api.searchResume(query: "auth race", projectID: "proj-1")
        XCTAssertEqual(hits.count, 1)
        let snippet = hits[0].snippet
        XCTAssertEqual(snippet.text, "fixed the auth race in worker")
        XCTAssertFalse(snippet.text.contains("\u{02}"))
        XCTAssertFalse(snippet.text.contains("\u{03}"))
        XCTAssertEqual(snippet.highlightRanges, [10..<19])
        XCTAssertEqual(snippet.spans.count, 3)
        XCTAssertEqual(snippet.spans[1].text, "auth race")
        XCTAssertTrue(snippet.spans[1].isHighlighted)
        XCTAssertFalse(snippet.spans[0].isHighlighted)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/search/resume")
        XCTAssertEqual(request.method, "POST")
        let body = try XCTUnwrap(request.bodyJSON)
        XCTAssertEqual(body["query"] as? String, "auth race")
        XCTAssertEqual(body["projectId"] as? String, "proj-1")
    }

    func testSnippetParserToleratesUnbalancedMarkers() {
        let dangling = HighlightedSnippet.parse("open \u{02}until the end")
        XCTAssertEqual(dangling.text, "open until the end")
        XCTAssertEqual(dangling.highlightRanges, [5..<18])

        let stray = HighlightedSnippet.parse("stray\u{03} close")
        XCTAssertEqual(stray.text, "stray close")
        XCTAssertTrue(stray.highlightRanges.isEmpty)

        let empty = HighlightedSnippet.parse("")
        XCTAssertEqual(empty.text, "")
        XCTAssertTrue(empty.spans.isEmpty)
    }

    // MARK: - Billing

    func testCheckoutUnwrapsURL() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"data":{"url":"https://checkout.stripe.com/c/pay/cs_test_123"}}"#)

        let result = try await api.checkout(plan: .pro)
        XCTAssertEqual(result.url?.absoluteString, "https://checkout.stripe.com/c/pay/cs_test_123")
        XCTAssertFalse(result.alreadySubscribed)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/billing/checkout")
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.bodyJSON?["plan"] as? String, "pro")
    }

    func testCheckoutAlreadySubscribed() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"data":{"alreadySubscribed":true,"plan":"pro"}}"#)
        let result = try await api.checkout(plan: .pro)
        XCTAssertNil(result.url)
        XCTAssertTrue(result.alreadySubscribed)
        XCTAssertEqual(result.plan, "pro")
    }

    func testBillingPortalURLUnwrapAndMissingAccountIsDark() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"data":{"url":"https://billing.stripe.com/p/session_abc"}}"#)
        let url = try await api.billingPortalURL()
        XCTAssertEqual(url.absoluteString, "https://billing.stripe.com/p/session_abc")
        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/billing/portal")
        XCTAssertEqual(request.method, "POST")

        // 404 when there is no billing account for the user.
        StubURLProtocol.enqueue(status: 404, json: #"{"success":false,"error":"no billing account"}"#)
        do {
            _ = try await api.billingPortalURL()
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .featureDark = error else {
                return XCTFail("wrong error: \(error)")
            }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    // MARK: - Retry and auth plumbing

    func testReadsRetryOn5xxThenSucceed() async throws {
        StubURLProtocol.enqueue(status: 502, json: "bad gateway")
        StubURLProtocol.enqueue(status: 502, json: "bad gateway")
        StubURLProtocol.enqueue(json: #"{"success":true,"skills":[]}"#)
        let skills = try await api.skillsLibrary()
        XCTAssertTrue(skills.isEmpty)
        XCTAssertEqual(StubURLProtocol.requests.count, 3)
    }

    func testSignedOutThrowsUnauthorizedWithoutNetworkCall() async {
        let signedOut = CloudProAPI(
            baseURL: URL(string: "https://cloud.test")!,
            configuration: StubURLProtocol.makeConfiguration(),
            accessTokenProvider: { nil }
        )
        do {
            _ = try await signedOut.skillsLibrary()
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .unauthorized = error else {
                return XCTFail("wrong error: \(error)")
            }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testFlagsFailOpenToEmptyOnError() async {
        StubURLProtocol.enqueue(status: 500, json: "boom")
        StubURLProtocol.enqueue(status: 500, json: "boom")
        StubURLProtocol.enqueue(status: 500, json: "boom")
        let flags = await api.flags()
        XCTAssertEqual(flags, [:])
    }
}
