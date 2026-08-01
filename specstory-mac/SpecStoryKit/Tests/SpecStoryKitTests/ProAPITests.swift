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
        XCTAssertEqual(first.clusterKey, "ck1")
        XCTAssertEqual(first.evidenceRefs, ["s1", "s2"])
        XCTAssertEqual(first.kind, "theme")
        XCTAssertTrue(first.isLatentTheme)

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

    // MARK: - Skill runs

    func testSkillRunsDecodeFullShape() async throws {
        // The runs listing answers {success, runs}: list and detail in one.
        StubURLProtocol.enqueue(json: """
        {"success":true,"runs":[
          {"id":"run-1","status":"judging","trigger":"manual","shardCount":2,"shardsDone":1,
           "sessionCount":40,"spanCount":320,
           "watermarkFrom":"2026-07-01T00:00:00Z","watermarkTo":"2026-07-30T00:00:00Z",
           "createdAt":"2026-07-30T12:00:00Z","startedAt":"2026-07-30T12:00:05Z",
           "endedAt":null,"updatedAt":"2026-07-30T12:04:00Z","error":null,"sessionsMined":34,
           "shards":[
             {"shardKey":"proj-a","scopeKind":"project","scope":{"project_id":"proj-a"},
              "status":"done","sandboxId":"sbx-1","sessionCount":21,"spanCount":180,
              "startedAt":"2026-07-30T12:00:10Z","endedAt":"2026-07-30T12:03:00Z","error":null},
             {"shardKey":"bucket:xyz","scopeKind":"bucket","scope":{"project_ids":["p1","p2","p3"]},
              "status":"reducing","sandboxId":"sbx-2","sessionCount":13,"spanCount":140,
              "startedAt":"2026-07-30T12:00:12Z","endedAt":null,"error":null}
           ],
           "dossierVerdicts":{"confirmed":2,"needs-edits":1,"unverified":1},
           "dossierTotal":4,
           "dossiers":[
             {"name":"prove-it-harness-setup","clusterKey":"ck-9","verdict":"confirmed",
              "confidence":0.9,"adjudicated":false,"hasSkill":true},
             {"name":"workflow-fan-out-review","clusterKey":"ck-4","verdict":"needs-edits",
              "confidence":0.62,"adjudicated":true,"hasSkill":false}
           ]}
        ]}
        """)

        let runs = try await api.skillRuns()
        XCTAssertEqual(runs.count, 1)
        let run = runs[0]
        XCTAssertEqual(run.id, "run-1")
        XCTAssertEqual(run.status, "judging")
        XCTAssertEqual(run.phase, .judging)
        XCTAssertTrue(run.isInProgress)
        XCTAssertEqual(run.trigger, "manual")
        XCTAssertEqual(run.shardCount, 2)
        XCTAssertEqual(run.shardsDone, 1)
        XCTAssertEqual(run.sessionsMined, 34)
        XCTAssertEqual(run.dossierTotal, 4)
        XCTAssertEqual(run.watermarkFrom, "2026-07-01T00:00:00Z")
        XCTAssertNil(run.endedAtDate)
        XCTAssertNotNil(run.startedAtDate)

        XCTAssertEqual(run.shards.count, 2)
        XCTAssertEqual(run.shards[0].scopeLabel, "proj-a")
        XCTAssertEqual(run.shards[0].phase, .done)
        XCTAssertEqual(run.shards[1].scopeLabel, "3 projects")
        XCTAssertEqual(run.shards[1].sandboxId, "sbx-2")

        XCTAssertEqual(run.dossiers.count, 2)
        XCTAssertEqual(run.dossiers[0].verdict, "confirmed")
        XCTAssertTrue(run.dossiers[0].hasSkill)
        XCTAssertTrue(run.dossiers[1].adjudicated)
        XCTAssertFalse(run.dossiers[1].hasSkill)

        XCTAssertEqual(run.verdictTallies.map(\.verdict), ["confirmed", "needs-edits", "unverified"])
        XCTAssertEqual(run.verdictTallies.map(\.count), [2, 1, 1])

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/runs")
        XCTAssertEqual(request.header("Authorization"), "Bearer test-access-token")
    }

    func testSkillRunsTolerateMinimalRowAndNullScope() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"runs":[
          {"id":"run-2","status":"done",
           "shards":[{"shardKey":"__all__","scopeKind":"owner","scope":null,"status":"done"}]}
        ]}
        """)
        let runs = try await api.skillRuns()
        XCTAssertEqual(runs.count, 1)
        let run = runs[0]
        XCTAssertEqual(run.shardCount, 0)
        XCTAssertEqual(run.sessionsMined, 0)
        XCTAssertTrue(run.dossiers.isEmpty)
        XCTAssertTrue(run.dossierVerdicts.isEmpty)
        XCTAssertTrue(run.verdictTallies.isEmpty)
        XCTAssertFalse(run.isInProgress)
        // A null scope falls back to the shard key for the label.
        XCTAssertEqual(run.shards[0].scopeLabel, "__all__")
    }

    func testSkillRunDurationRules() {
        // Terminal runs freeze at endedAt ?? updatedAt; without the fallback
        // the timer would tick forever on failed runs that never set endedAt.
        let failed = SkillRun(
            id: "r", status: "failed",
            startedAt: "2026-07-30T12:00:00Z", endedAt: nil, updatedAt: "2026-07-30T12:05:00Z"
        )
        let farFuture = Date(timeIntervalSince1970: 4_000_000_000)
        XCTAssertEqual(failed.duration(now: farFuture), 300)

        let done = SkillRun(
            id: "r2", status: "done",
            startedAt: "2026-07-30T12:00:00Z", endedAt: "2026-07-30T12:02:00Z",
            updatedAt: "2026-07-30T12:09:00Z"
        )
        XCTAssertEqual(done.duration(now: farFuture), 120)

        // In-progress runs tick live against now.
        let running = SkillRun(id: "r3", status: "running", startedAt: "2026-07-30T12:00:00Z")
        let start = CloudDate.parse("2026-07-30T12:00:00Z")!
        XCTAssertEqual(running.duration(now: start.addingTimeInterval(42)), 42)

        // No timestamps at all: no duration rather than a garbage value.
        XCTAssertNil(SkillRun(id: "r4", status: "queued").duration(now: farFuture))

        // Shards follow the same rule; a reducing shard keeps ticking.
        let reducing = SkillRunShard(shardKey: "__all__", status: "reducing",
                                     startedAt: "2026-07-30T12:00:00Z")
        let shardStart = CloudDate.parse("2026-07-30T12:00:00Z")!
        XCTAssertEqual(reducing.duration(now: shardStart.addingTimeInterval(9)), 9)
        let doneShard = SkillRunShard(shardKey: "p", status: "done",
                                      startedAt: "2026-07-30T12:00:00Z",
                                      endedAt: "2026-07-30T12:01:30Z")
        XCTAssertEqual(doneShard.duration(now: farFuture), 90)
    }

    func testSkillRunPhaseGatingMatchesWebClient() {
        // RUN_IN_PROGRESS_STATUSES is exactly these four.
        for status in ["queued", "sharding", "running", "judging"] {
            XCTAssertTrue(SkillRunPhase(status: status).isRunInProgress, status)
        }
        for status in ["reducing", "done", "failed", "", "mystery"] {
            XCTAssertFalse(SkillRunPhase(status: status).isRunInProgress, status)
        }
        XCTAssertEqual(SkillRunPhase(status: "DONE"), .done)
        XCTAssertEqual(SkillRunPhase(status: "mystery"), .unknown)
        XCTAssertTrue(SkillRunPhase(status: "failed").isTerminal)
        XCTAssertFalse(SkillRunPhase(status: "reducing").isTerminal)
    }

    func testTriggerSkillRunPostsAndReturnsRunId() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"runId":"run-9"}"#)
        let runId = try await api.triggerSkillRun()
        XCTAssertEqual(runId, "run-9")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/run")
        XCTAssertEqual(request.method, "POST")
    }

    func testTriggerSkillRunConflictMapsToRunAlreadyInProgress() async {
        StubURLProtocol.enqueue(status: 409, json: #"{"success":false,"error":"run already in progress for owner u-1"}"#)
        do {
            _ = try await api.triggerSkillRun()
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .runAlreadyInProgress = error else {
                return XCTFail("wrong error: \(error)")
            }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    func testTriggerSkillRunForbiddenMapsToUpgradeRequired() async {
        // 403 covers both upgrade_required and the LORE_RUN_ENABLED kill switch.
        StubURLProtocol.enqueue(status: 403, json: #"{"success":false,"error":"lore runs disabled (LORE_RUN_ENABLED off)"}"#)
        do {
            _ = try await api.triggerSkillRun()
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .upgradeRequired = error else {
                return XCTFail("wrong error: \(error)")
            }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    // MARK: - Review queue

    func testPendingDossiersSendsStatusQueryAndDecodes() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"dossiers":[
          {"id":"d-1","name":"workflow-fan-out-review","clusterKey":"ck-4","status":"pending",
           "verdict":"needs-edits","confidence":0.62,"skillMd":"# Fan-out review",
           "createdAt":"2026-07-28T10:00:00Z"},
          {"id":"d-2","clusterKey":"ck-7","status":"pending"}
        ]}
        """)

        let dossiers = try await api.pendingDossiers()
        XCTAssertEqual(dossiers.count, 2)
        XCTAssertEqual(dossiers[0].id, "d-1")
        XCTAssertEqual(dossiers[0].displayName, "workflow-fan-out-review")
        XCTAssertEqual(dossiers[0].confidence, 0.62)
        XCTAssertEqual(dossiers[0].skillMd, "# Fan-out review")
        // Unnamed review dossiers fall back to the cluster key.
        XCTAssertEqual(dossiers[1].displayName, "ck-7")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/dossiers")
        XCTAssertEqual(request.url.query, "status=pending")
    }

    func testReviewDossierApprovePatchesAction() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true}"#)
        try await api.reviewDossier(id: "d-1", action: .approve)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/dossiers/d-1")
        XCTAssertEqual(request.method, "PATCH")
        XCTAssertEqual(request.bodyJSON?["action"] as? String, "approve")
        XCTAssertNil(request.bodyJSON?["note"])
    }

    func testReviewDossierDeclineSendsEmptyNote() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true}"#)
        try await api.reviewDossier(id: "d-2", action: .decline, note: "")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/dossiers/d-2")
        XCTAssertEqual(request.bodyJSON?["action"] as? String, "decline")
        XCTAssertEqual(request.bodyJSON?["note"] as? String, "")
    }

    func testReviewDossierNotOwnerMapsToUpgradeRequiredShape() async {
        // The worker answers 403 for a foreign dossier; it surfaces through
        // the same mapping as the entitlement gate.
        StubURLProtocol.enqueue(status: 403, json: #"{"success":false,"error":"forbidden"}"#)
        do {
            try await api.reviewDossier(id: "d-3", action: .decline, note: "")
            XCTFail("expected error")
        } catch let error as CloudProError {
            guard case .upgradeRequired = error else {
                return XCTFail("wrong error: \(error)")
            }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }

    // MARK: - Forged skills down-channel

    func testForgedSkillsSendsInstallStateAndDecodes() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"skills":[
          {"name":"prove-it-harness-setup","status":"active","skillMd":"# Prove it",
           "clusterKey":"ck-9","fingerprint":"fp-1","contentSha":"sha-1",
           "installState":"available","recommendation":"up-to-date"},
          {"name":"vendored-cli-refresh","installState":"installed",
           "recommendation":{"recommendation":"minor-drift","reason":"evidence grew"}}
        ]}
        """)

        let skills = try await api.forgedSkills(installState: "available")
        XCTAssertEqual(skills.count, 2)
        XCTAssertEqual(skills[0].name, "prove-it-harness-setup")
        XCTAssertEqual(skills[0].contentSha, "sha-1")
        XCTAssertEqual(skills[0].recommendation, "up-to-date")
        XCTAssertFalse(skills[0].isInstalled)
        // Structured recommendation objects flatten to their badge string.
        XCTAssertEqual(skills[1].recommendation, "minor-drift")
        XCTAssertTrue(skills[1].isInstalled)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/skills")
        XCTAssertEqual(request.url.query, "install_state=available")
    }

    func testForgedSkillsOmitsQueryWhenUnfiltered() async throws {
        StubURLProtocol.enqueue(json: #"{"success":true,"skills":[]}"#)
        _ = try await api.forgedSkills()
        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url.path, "/api/v1/lore/skills")
        XCTAssertNil(request.url.query)
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
