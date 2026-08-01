import XCTest
@testable import SpecStoryKit

/// Decode and error-mapping tests for CloudAnalyticsAPI against response JSON
/// transcribed from the cloud worker's analytics handlers.
final class CloudAnalyticsTests: XCTestCase {
    private var api: CloudAnalyticsAPI!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        api = CloudAnalyticsAPI(
            baseURL: URL(string: "https://cloud.test")!,
            configuration: StubURLProtocol.makeConfiguration(),
            accessTokenProvider: { "test-access-token" }
        )
    }

    // MARK: - Overview

    func testOverviewDecodesFullPayload() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"overview":{
          "meta":{
            "range":"30d","fromIso":"2026-07-02T00:00:00Z","toIso":"2026-08-01T00:00:00Z",
            "previousFromIso":"2026-06-02T00:00:00Z","previousToIso":"2026-07-02T00:00:00Z",
            "timezone":"America/New_York","generatedAtIso":"2026-08-01T12:00:00Z",
            "coverage":{"totalSessions":212,"sessionsWithExchanges":198,"zeroExchangeSessions":14,
              "nullTimestampSessions":2,"cursorZeroWidthSessions":9,"durationEligibleSessions":180,
              "durationEligibleCoverage":0.85,"distinctAgentCount":4,"distinctProjectCount":7}
          },
          "agents":[
            {"agentName":"claude-code","sessionCount":120,"exchangeCount":2210,"lastUsedIso":"2026-07-31T22:10:00Z"},
            {"agentName":"Unknown","sessionCount":12,"exchangeCount":40,"lastUsedIso":null}
          ],
          "agentTotals":{"totalSessions":212,"totalExchanges":3847,
            "byAgent":[{"agentName":"claude-code","sessionCount":120,"pct":0.57}]},
          "sessionsByDayByAgent":[
            {"date":"2026-07-02","total":3,"perAgent":{"claude-code":2,"Unknown":1}},
            {"date":"2026-07-03","total":0,"perAgent":{}}
          ],
          "punchcard":{"timezone":"America/New_York",
            "cells":[[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],
                     [0,0,0,0,0,0,0,0,0,1,2,3,1,0,9,0,0,0,0,0,0,0,0,0],
                     [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],
                     [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],
                     [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],
                     [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],
                     [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]],
            "peakWindow":{"dayOfWeek":1,"hourOfDay":14,"count":9,"label":"Mon 14:00"}},
          "durationsByAgent":[{"agentName":"claude-code","eligibleSessions":110,"medianMs":5400000,"p90Ms":7800000,"totalMs":594000000}],
          "messages":{"totalExchanges":3847,"avgExchangesPerSession":18.4,
            "depthDistribution":[
              {"bucket":"1","sessionCount":20},{"bucket":"2-5","sessionCount":48},
              {"bucket":"6-10","sessionCount":52},{"bucket":"11-25","sessionCount":56},
              {"bucket":"26-50","sessionCount":24},{"bucket":"50+","sessionCount":12}],
            "turnsByAgent":[{"agentName":"claude-code","exchangeCount":2210}],
            "longestSession":{"sessionId":"s-1","title":"Big refactor","agentName":"claude-code","exchangeCount":142}},
          "activity":{"activeDays":24,"currentStreak":12,"longestStreak":21},
          "concurrencyPeak":{"eligibleSessions":180,"peak":4,"peakAtIso":"2026-07-14T15:00:00Z"},
          "specDrivenStart":{"version":"v0-heuristic","rate":0.69,"sessionsWithFirstPrompt":180,
            "passingSessions":124,"previousRate":0.61,"trendDelta":0.08},
          "topProjects":[{"projectId":"p-1","projectName":"getspecstory","sessionCount":64,
            "exchangeCount":1204,"lastActiveIso":"2026-07-31T20:00:00Z"}],
          "highlights":[
            {"id":"sessions","label":"Sessions","current":212,"previous":140,"deltaAbs":72,"deltaPct":0.51,"direction":"up"},
            {"id":"messages","label":"Messages","current":3847,"previous":4210,"deltaAbs":-363,"deltaPct":-0.09,"direction":"down"},
            {"id":"specRate","label":"Spec-driven","current":69,"previous":61,"deltaAbs":8,"deltaPct":0.13,"direction":"up"},
            {"id":"activeDays","label":"Active days","current":24,"previous":24,"deltaAbs":0,"deltaPct":null,"direction":"flat"}],
          "activityCalendar":[{"date":"2026-07-31","count":3},{"date":"2026-08-01","count":1}],
          "spend":null
        }}}
        """)

        let overview = try await api.overview(range: .month, timezone: "America/New_York")

        XCTAssertEqual(overview.meta.range, "30d")
        XCTAssertEqual(overview.meta.coverage.totalSessions, 212)
        XCTAssertEqual(overview.meta.coverage.durationEligibleCoverage, 0.85, accuracy: 0.0001)
        XCTAssertNotNil(overview.meta.fromDate)
        XCTAssertEqual(overview.agents.count, 2)
        XCTAssertEqual(overview.agents[0].agentName, "claude-code")
        XCTAssertNil(overview.agents[1].lastUsedIso)
        XCTAssertEqual(overview.agentTotals.byAgent.first?.pct ?? 0, 0.57, accuracy: 0.0001)
        XCTAssertEqual(overview.sessionsByDayByAgent.count, 2)
        XCTAssertEqual(overview.sessionsByDayByAgent[0].perAgent["claude-code"], 2)
        XCTAssertNotNil(overview.sessionsByDayByAgent[0].dayDate)
        XCTAssertEqual(overview.punchcard.cells.count, 7)
        XCTAssertEqual(overview.punchcard.count(dayOfWeek: 1, hour: 14), 9)
        XCTAssertEqual(overview.punchcard.maxCount, 9)
        XCTAssertEqual(overview.punchcard.peakWindow?.label, "Mon 14:00")
        XCTAssertEqual(overview.durationsByAgent.first?.medianMs ?? 0, 5_400_000, accuracy: 0.5)
        XCTAssertEqual(overview.messages.totalExchanges, 3847)
        XCTAssertEqual(overview.messages.avgExchangesPerSession, 18.4, accuracy: 0.0001)
        XCTAssertEqual(overview.messages.depthDistribution.count, 6)
        XCTAssertEqual(overview.messages.longestSession?.exchangeCount, 142)
        XCTAssertEqual(overview.activity.currentStreak, 12)
        XCTAssertEqual(overview.activity.longestStreak, 21)
        XCTAssertEqual(overview.concurrencyPeak.peak, 4)
        XCTAssertEqual(overview.specDrivenStart.rate ?? 0, 0.69, accuracy: 0.0001)
        XCTAssertEqual(overview.specDrivenStart.trendDelta ?? 0, 0.08, accuracy: 0.0001)
        XCTAssertEqual(overview.topProjects.first?.projectName, "getspecstory")
        XCTAssertEqual(overview.highlights.count, 4)
        XCTAssertTrue(overview.highlights[0].isUp)
        XCTAssertTrue(overview.highlights[1].isDown)
        XCTAssertNil(overview.highlights[3].deltaPct)
        XCTAssertEqual(overview.activityCalendar.count, 2)
        XCTAssertFalse(overview.isEmpty)

        // Query params: range plus the punchcard timezone.
        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertTrue(request.url.path.hasSuffix("/api/v1/analytics/overview"))
        let query = request.url.query ?? ""
        XCTAssertTrue(query.contains("range=30d"))
        XCTAssertTrue(query.contains("tz=America"))
        XCTAssertEqual(request.header("Authorization"), "Bearer test-access-token")
    }

    func testOverviewEmptyWorkspaceIsFullyShaped() async throws {
        // Empty workspaces answer a fully-shaped empty 200, never 401/500.
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"overview":{
          "meta":{"range":"7d","fromIso":"2026-07-25T00:00:00Z","toIso":"2026-08-01T00:00:00Z",
            "timezone":"UTC","coverage":{"totalSessions":0}},
          "agents":[],"sessionsByDayByAgent":[],"highlights":[],"activityCalendar":[]
        }}}
        """)

        let overview = try await api.overview(range: .week)
        XCTAssertTrue(overview.isEmpty)
        XCTAssertEqual(overview.activity.currentStreak, 0)
        XCTAssertNil(overview.punchcard.peakWindow)
        XCTAssertEqual(overview.messages.depthDistribution.count, 0)
    }

    // MARK: - Sessions

    func testSessionsDecodeAndWindowParams() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"sessions":[
          {"id":"db-1","clientId":"c-1","name":"Fix the flaky test","fileName":"2026-07-30_fix.md",
           "agentName":"claude-code","startedAt":"2026-07-30T14:00:00Z","endedAt":"2026-07-30T15:30:00Z",
           "updatedAt":"2026-07-30T15:31:00Z","bucketIso":"2026-07-30T14:00:00Z","exchangeCount":24,
           "projectId":"p-1","projectName":"getspecstory","projectColor":"#00b3a4",
           "firstPrompt":"Please fix the flaky watch test"},
          {"id":"db-2","clientId":"c-2","name":null,"fileName":null,"agentName":"Unknown",
           "startedAt":null,"endedAt":null,"updatedAt":"2026-07-29T10:00:00Z",
           "bucketIso":"2026-07-29T09:00:00Z","exchangeCount":0,
           "projectId":null,"projectName":null,"projectColor":null,"firstPrompt":null}
        ]}}
        """)

        let rows = try await api.sessions(fromIso: "2026-07-29T00:00:00Z", toIso: "2026-07-31T00:00:00Z", agent: "claude-code")
        XCTAssertEqual(rows.count, 2)
        XCTAssertEqual(rows[0].name, "Fix the flaky test")
        XCTAssertEqual(rows[0].exchangeCount, 24)
        XCTAssertNotNil(rows[0].bucketDate)
        XCTAssertEqual(rows[1].agentName, "Unknown")
        XCTAssertNil(rows[1].name)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertTrue(request.url.path.hasSuffix("/api/v1/analytics/sessions"))
        let query = request.url.query ?? ""
        XCTAssertTrue(query.contains("from=2026-07-29"))
        XCTAssertTrue(query.contains("to=2026-07-31"))
        XCTAssertTrue(query.contains("agent=claude-code"))
    }

    // MARK: - Ledger

    func testLedgerDecodesAndToleratesNumericStrings() async throws {
        // estCostUsd arrives as a numeric string here: pg-string tolerance.
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"ledger":{
          "meta":{"range":"30d","fromIso":"2026-07-02T00:00:00Z","toIso":"2026-08-01T00:00:00Z",
            "timezone":"UTC","generatedAtIso":"2026-08-01T12:00:00Z"},
          "totals":{"estCostUsd":"1429.43","cacheSavingsUsd":301.2,"inputTokens":1250000,
            "outputTokens":890000,"cacheCreationTokens":3000000,"cacheReadTokens":"12500000",
            "totalTokens":17640000},
          "spendByDay":[
            {"date":"2026-07-30","estCostUsd":48.2,"inputTokens":52000,"outputTokens":31000,
             "cacheCreationTokens":110000,"cacheReadTokens":410000},
            {"date":"2026-07-31","estCostUsd":61.9,"inputTokens":66000,"outputTokens":39000,
             "cacheCreationTokens":150000,"cacheReadTokens":520000}],
          "spendByAgent":[{"agentName":"claude-code","estCostUsd":1105.1,"cacheSavingsUsd":260.4,"sessionCount":120}],
          "spendByProject":[{"projectId":"p-1","projectName":"getspecstory","estCostUsd":740.2,"sessionCount":64}],
          "coverage":{"totalSessions":212,"coveredSessions":170,"pricedSessions":150,
            "excludedByProvider":[{"provider":"cursor","sessionCount":30},{"provider":"copilot","sessionCount":12}]}
        }}}
        """)

        let ledger = try await api.ledger(range: .month)
        XCTAssertEqual(ledger.totals.estCostUsd, 1429.43, accuracy: 0.001)
        XCTAssertEqual(ledger.totals.cacheReadTokens, 12_500_000)
        XCTAssertEqual(ledger.spendByDay.count, 2)
        XCTAssertEqual(ledger.spendByDay[1].estCostUsd, 61.9, accuracy: 0.001)
        XCTAssertEqual(ledger.activeSpendDays, 2)
        XCTAssertEqual(ledger.spendByAgent.first?.sessionCount, 120)
        XCTAssertEqual(ledger.spendByProject.first?.projectName, "getspecstory")
        XCTAssertEqual(ledger.coverage.excludedByProvider.count, 2)
        XCTAssertEqual(ledger.coverage.excludedByProvider[0].provider, "cursor")
        XCTAssertFalse(ledger.isEmpty)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertTrue(request.url.path.hasSuffix("/api/v1/analytics/ledger"))
        XCTAssertTrue((request.url.query ?? "").contains("range=30d"))
    }

    // MARK: - Spec score

    func testSpecScoreDecodes() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"specScore":{
          "meta":{"range":"30d","fromIso":"2026-07-02T00:00:00Z","toIso":"2026-08-01T00:00:00Z",
            "timezone":"UTC","generatedAtIso":"2026-08-01T12:00:00Z",
            "scorerVersion":"specscore-4","llmScorerVersion":"specscore-llm-3"},
          "startRate":{"rate":0.6889,"passingSessions":124,"sessionsWithFirstPrompt":180,"totalScored":198},
          "startRateTrend":[
            {"date":"2026-07-30","rate":0.75,"passingSessions":6,"sessionsWithFirstPrompt":8},
            {"date":"2026-07-31","rate":null,"passingSessions":0,"sessionsWithFirstPrompt":0}],
          "gradeDistribution":[
            {"grade":"A","sessionCount":38},{"grade":"B","sessionCount":86},
            {"grade":"C","sessionCount":42},{"grade":"D","sessionCount":20},
            {"grade":"F","sessionCount":12}],
          "perDimension":[
            {"dimension":"constraints","passCount":96,"passRate":0.53},
            {"dimension":"success_criteria","passCount":72,"passRate":0.4},
            {"dimension":"verification","passCount":84,"passRate":0.47},
            {"dimension":"context","passCount":150,"passRate":0.83},
            {"dimension":"specificity","passCount":132,"passRate":0.73}],
          "offendingSamples":[
            {"sessionId":"s-9","clientId":"c-9","score":8,"grade":"F","scoredBy":"rubric",
             "rationale":null,"llmIntent":null,"dimEvidence":null,"duplicateCount":3,
             "agentName":"cursor","projectId":null,"firstPromptExcerpt":"fix it",
             "missingDims":["constraints","verification"],"bucketIso":"2026-07-28T09:00:00Z"}],
          "exemplarSamples":[
            {"sessionId":"s-2","clientId":"c-2","score":88,"grade":"A","scoredBy":"llm",
             "rationale":"Clear goal with verification steps.","llmIntent":"implementation",
             "dimEvidence":{"goal_clarity":{"checks":[{"id":"G1","answer":true,"evidence":"add a retry"}],
               "assessment":"Goal is explicit."}},
             "duplicateCount":1,"agentName":"claude-code","projectId":"p-1",
             "firstPromptExcerpt":"Add a retry with backoff to the sync loop...",
             "missingDims":[],"bucketIso":"2026-07-30T14:00:00Z"}]
        }}}
        """)

        let score = try await api.specScore(range: .month)
        XCTAssertEqual(score.meta.scorerVersion, "specscore-4")
        XCTAssertEqual(score.startRate.rate ?? 0, 0.6889, accuracy: 0.0001)
        XCTAssertEqual(score.startRate.totalScored, 198)
        XCTAssertEqual(score.startRateTrend.count, 2)
        XCTAssertNil(score.startRateTrend[1].rate)
        XCTAssertEqual(score.gradeDistribution.count, 5)
        XCTAssertEqual(score.perDimension.count, 5)
        XCTAssertEqual(score.offendingSamples.first?.duplicateCount, 3)
        XCTAssertEqual(score.offendingSamples.first?.missingDims, ["constraints", "verification"])
        let exemplar = try XCTUnwrap(score.exemplarSamples.first)
        XCTAssertEqual(exemplar.grade, "A")
        XCTAssertEqual(exemplar.scoredBy, "llm")
        XCTAssertEqual(exemplar.dimEvidence?["goal_clarity"]?.checks.first?.answer, true)
        XCTAssertFalse(score.isEmpty)
        // Count-weighted typical grade: (38*4+86*3+42*2+20*1+12*0)/198 = 2.6 -> B.
        XCTAssertEqual(score.aggregateGrade, "B")
    }

    func testAggregateGradeEdges() {
        XCTAssertNil(AnalyticsSpecScore.aggregateGrade(from: []))
        XCTAssertNil(AnalyticsSpecScore.aggregateGrade(from: [AnalyticsGradeCount(grade: "A", sessionCount: 0)]))
        XCTAssertEqual(AnalyticsSpecScore.aggregateGrade(from: [AnalyticsGradeCount(grade: "A", sessionCount: 5)]), "A")
        XCTAssertEqual(AnalyticsSpecScore.aggregateGrade(from: [
            AnalyticsGradeCount(grade: "A", sessionCount: 1),
            AnalyticsGradeCount(grade: "F", sessionCount: 1),
        ]), "C")
    }

    func testSpecScoreSessionsPageDecodes() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"specScoreSessions":{
          "range":"30d","grade":"B","total":86,"offset":10,"limit":10,
          "sessions":[{"sessionId":"s-3","clientId":"c-3","score":64,"grade":"B","scoredBy":"rubric",
            "duplicateCount":1,"agentName":"codex","missingDims":["verification"]}]
        }}}
        """)

        let page = try await api.specScoreSessions(range: .month, grade: "B", limit: 10, offset: 10)
        XCTAssertEqual(page.total, 86)
        XCTAssertEqual(page.offset, 10)
        XCTAssertEqual(page.sessions.first?.score, 64)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertTrue(request.url.path.hasSuffix("/api/v1/analytics/spec-score/sessions"))
        let query = request.url.query ?? ""
        XCTAssertTrue(query.contains("grade=B"))
        XCTAssertTrue(query.contains("limit=10"))
        XCTAssertTrue(query.contains("offset=10"))
    }

    // MARK: - Processing

    func testProcessingDecodes() async throws {
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"processing":{
          "pending":3,"stalled":1,"derivedThroughIso":"2026-08-01T11:48:00Z","generatedAtIso":"2026-08-01T12:00:00Z"
        }}}
        """)

        let processing = try await api.processing()
        XCTAssertEqual(processing.pending, 3)
        XCTAssertEqual(processing.stalled, 1)
        XCTAssertNotNil(processing.derivedThroughDate)
    }

    // MARK: - Gate mapping

    func testForbiddenMapsToUpgradeRequired() async {
        StubURLProtocol.enqueue(status: 403, json: """
        {"success":false,"error":"upgrade_required","data":{"feature":"analytics"}}
        """)

        do {
            _ = try await api.overview(range: .month)
            XCTFail("expected upgradeRequired")
        } catch let error as CloudAnalyticsError {
            guard case .upgradeRequired(let feature) = error else {
                return XCTFail("expected upgradeRequired, got \(error)")
            }
            XCTAssertEqual(feature, "analytics")
        } catch {
            XCTFail("unexpected error \(error)")
        }
    }

    func testNotFoundMapsToFeatureDark() async {
        StubURLProtocol.enqueue(status: 404, json: "not found")

        do {
            _ = try await api.ledger(range: .week)
            XCTFail("expected featureDark")
        } catch let error as CloudAnalyticsError {
            guard case .featureDark = error else {
                return XCTFail("expected featureDark, got \(error)")
            }
        } catch {
            XCTFail("unexpected error \(error)")
        }
    }

    func testUnauthorizedMaps() async {
        StubURLProtocol.enqueue(status: 401, json: "Unauthorized")

        do {
            _ = try await api.processing()
            XCTFail("expected unauthorized")
        } catch let error as CloudAnalyticsError {
            guard case .unauthorized = error else {
                return XCTFail("expected unauthorized, got \(error)")
            }
        } catch {
            XCTFail("unexpected error \(error)")
        }
    }

    func testReadsRetryOn5xxThenSucceed() async throws {
        StubURLProtocol.enqueue(status: 500, json: "boom")
        StubURLProtocol.enqueue(status: 429, json: "slow down")
        StubURLProtocol.enqueue(json: """
        {"success":true,"data":{"processing":{"pending":0,"stalled":0}}}
        """)

        let processing = try await api.processing()
        XCTAssertEqual(processing.pending, 0)
        XCTAssertEqual(StubURLProtocol.requests.count, 3)
    }

    func testNilTokenMeansUnauthorizedBeforeAnyRequest() async {
        let signedOut = CloudAnalyticsAPI(
            baseURL: URL(string: "https://cloud.test")!,
            configuration: StubURLProtocol.makeConfiguration(),
            accessTokenProvider: { nil }
        )
        do {
            _ = try await signedOut.overview(range: .month)
            XCTFail("expected unauthorized")
        } catch let error as CloudAnalyticsError {
            guard case .unauthorized = error else {
                return XCTFail("expected unauthorized, got \(error)")
            }
            XCTAssertTrue(StubURLProtocol.requests.isEmpty)
        } catch {
            XCTFail("unexpected error \(error)")
        }
    }

    // MARK: - Range plumbing

    func testRangeMetadata() {
        XCTAssertEqual(AnalyticsRange.week.rawValue, "7d")
        XCTAssertEqual(AnalyticsRange.month.dayCount, 30)
        XCTAssertEqual(AnalyticsRange.quarter.displayName, "90 days")
        XCTAssertEqual(AnalyticsRange.allCases.count, 3)
    }

    func testAnalyticsDayRoundTrip() {
        let date = AnalyticsDay.parse("2026-07-30")
        XCTAssertNotNil(date)
        XCTAssertEqual(AnalyticsDay.key(for: date!), "2026-07-30")
        XCTAssertNil(AnalyticsDay.parse(nil))
        XCTAssertNil(AnalyticsDay.parse(""))
    }
}
