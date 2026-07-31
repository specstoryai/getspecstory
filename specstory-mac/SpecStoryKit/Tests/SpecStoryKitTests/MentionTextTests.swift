import XCTest
@testable import SpecStoryKit

final class MentionTextTests: XCTestCase {
    // MARK: - activeMentionQuery

    func testActiveQueryAtEndOfText() {
        XCTAssertEqual(activeMentionQuery("hello @cl", caret: 9), "cl")
    }

    func testBareAtOpensWithEmptyQuery() {
        XCTAssertEqual(activeMentionQuery("@", caret: 1), "")
        XCTAssertEqual(activeMentionQuery("hi @", caret: 4), "")
    }

    func testMidTextCaretUsesPrefixUpToCaret() {
        // Caret sits after "@cl" inside a longer token; only the prefix counts.
        XCTAssertEqual(activeMentionQuery("hello @claude", caret: 9), "cl")
        // Caret after a completed token mid-sentence.
        XCTAssertEqual(activeMentionQuery("ask @cl about auth", caret: 7), "cl")
    }

    func testCaretBeforeAtSeesNoToken() {
        XCTAssertNil(activeMentionQuery("ask @cl about auth", caret: 4))
        XCTAssertNil(activeMentionQuery("@abc", caret: 0))
    }

    func testAtInsideWordRejected() {
        // No preceding whitespace: an email-style @ never opens the popover.
        XCTAssertNil(activeMentionQuery("email me@example", caret: 16))
        XCTAssertNil(activeMentionQuery("a@b", caret: 3))
    }

    func testSecondAtResetsToken() {
        // The token char class forbids @, so "@foo@" has no active token.
        XCTAssertNil(activeMentionQuery("@foo@bar", caret: 8))
        // A fresh token after whitespace is active again.
        XCTAssertEqual(activeMentionQuery("@foo @bar", caret: 9), "bar")
    }

    func testWhitespaceEndsToken() {
        XCTAssertNil(activeMentionQuery("@foo ", caret: 5))
        XCTAssertNil(activeMentionQuery("@foo bar", caret: 8))
    }

    func testCaretBeyondBoundsClamps() {
        XCTAssertEqual(activeMentionQuery("@abc", caret: 99), "abc")
        XCTAssertNil(activeMentionQuery("@abc", caret: -1))
    }

    // MARK: - stripActiveMention

    func testStripRemovesTokenKeepsPrecedingSpace() {
        XCTAssertEqual(stripActiveMention("hello @cl", caret: 9), "hello ")
    }

    func testStripAtStartOfTextRemovesEverything() {
        XCTAssertEqual(stripActiveMention("@cl", caret: 3), "")
    }

    func testStripPreservesTextAfterCaret() {
        // upToCaret "hi @cl" loses "@cl", keeps its space; " world" survives.
        XCTAssertEqual(stripActiveMention("hi @cl world", caret: 6), "hi  world")
        // Caret mid-token: the tail of the token stays, like the web client.
        XCTAssertEqual(stripActiveMention("hello @claude", caret: 9), "hello aude")
    }

    func testStripWithoutActiveMentionIsIdentity() {
        XCTAssertEqual(stripActiveMention("plain text", caret: 10), "plain text")
        XCTAssertEqual(stripActiveMention("me@example", caret: 10), "me@example")
        XCTAssertEqual(stripActiveMention("", caret: 0), "")
    }

    func testStripNewlineCountsAsWhitespace() {
        XCTAssertEqual(stripActiveMention("line\n@cl", caret: 8), "line\n")
    }

    // MARK: - mentionCandidates

    private let projects: [(id: String, name: String)] = [
        (id: "p1", name: "rookery"),
        (id: "p2", name: "getspecstory"),
        (id: "p3", name: "specstory-cli"),
    ]
    private let agents = ["Claude Code", "Codex", "Cursor"]

    private func candidates(
        selectedProjects: Set<String> = [],
        selectedAgents: Set<String> = [],
        timeSelected: Bool = false,
        query: String = ""
    ) -> [MentionItem] {
        mentionCandidates(
            projects: projects, agents: agents,
            selectedProjects: selectedProjects, selectedAgents: selectedAgents,
            timeSelected: timeSelected, query: query
        )
    }

    func testEmptyQueryListsAllUnselectedInKindOrder() {
        XCTAssertEqual(candidates().map(\.id), [
            "project-p1", "project-p2", "project-p3",
            "agent-Claude Code", "agent-Codex", "agent-Cursor",
            "time-today", "time-week", "time-month",
        ])
    }

    func testTimeMentionLabels() {
        let times = candidates().filter { $0.kind == .time }
        XCTAssertEqual(times.map(\.label), ["Today", "Past week", "Past month"])
        XCTAssertEqual(times.map(\.value), ["today", "week", "month"])
    }

    func testSelectedValuesAreFilteredOut() {
        let items = candidates(selectedProjects: ["p2"], selectedAgents: ["Codex"], timeSelected: true)
        XCTAssertFalse(items.contains { $0.id == "project-p2" })
        XCTAssertFalse(items.contains { $0.id == "agent-Codex" })
        XCTAssertFalse(items.contains { $0.kind == .time })
        XCTAssertEqual(items.count, 4)
    }

    func testPrefixRanksAboveContainsWithinAKind() {
        // "specstory-cli" is a prefix match, "getspecstory" only contains.
        XCTAssertEqual(candidates(query: "spec").map(\.value), ["p3", "p2"])
    }

    func testMatchIsCaseInsensitive() {
        XCTAssertEqual(candidates(query: "SPEC").map(\.value), ["p3", "p2"])
        XCTAssertEqual(candidates(query: "toDay").map(\.id), ["time-today"])
    }

    func testKindOrderBeatsMatchRankAcrossKinds() {
        // Project contains-match still lists before agent prefix-match.
        XCTAssertEqual(candidates(query: "cl").map(\.id), ["project-p3", "agent-Claude Code"])
    }

    func testNoMatchesYieldsEmpty() {
        XCTAssertEqual(candidates(query: "zzz"), [])
    }

    func testDuplicateInputsCollapse() {
        let items = mentionCandidates(
            projects: [(id: "p1", name: "rookery"), (id: "p1", name: "rookery")],
            agents: ["Codex", "Codex"],
            selectedProjects: [], selectedAgents: [], timeSelected: true, query: ""
        )
        XCTAssertEqual(items.map(\.id), ["project-p1", "agent-Codex"])
    }

    func testResultCapsAtTwelve() {
        let many = (1...20).map { (id: "p\($0)", name: "project \($0)") }
        let items = mentionCandidates(
            projects: many, agents: agents,
            selectedProjects: [], selectedAgents: [], timeSelected: false, query: ""
        )
        XCTAssertEqual(items.count, 12)
        XCTAssertTrue(items.allSatisfy { $0.kind == .project }, "cap applies before later kinds fit")
    }
}
