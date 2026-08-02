import XCTest
@testable import SpecStoryKit

final class NativeSessionInspectorTests: XCTestCase {
    private var tempDir: URL!

    override func setUpWithError() throws {
        tempDir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("inspector-tests-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: tempDir)
    }

    // MARK: - Fixture plumbing

    @discardableResult
    private func writeClaudeFixture(_ lines: [String], sessionID: String = "abc-123", projectDir: String = "-Users-dev-proj") throws -> URL {
        let dir = tempDir.appendingPathComponent("projects").appendingPathComponent(projectDir)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let url = dir.appendingPathComponent(sessionID + ".jsonl")
        try (lines.joined(separator: "\n") + "\n").write(to: url, atomically: true, encoding: .utf8)
        return url
    }

    private var projectsRoot: URL { tempDir.appendingPathComponent("projects") }

    private let headerLine = #"{"type":"user","isSidechain":false,"cwd":"/Users/dev/proj","gitBranch":"feature/x","timestamp":"2026-07-31T10:00:00.000Z","message":{"role":"user","content":"please fix the parser"}}"#

    private func assistantToolUse(id: String, name: String, inputJSON: String, model: String = "claude-opus-4-7", usage: String = #"{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30}"#, sidechain: Bool = false, timestamp: String = "2026-07-31T10:01:00.000Z") -> String {
        #"{"type":"assistant","isSidechain":\#(sidechain),"timestamp":"\#(timestamp)","message":{"role":"assistant","model":"\#(model)","content":[{"type":"tool_use","id":"\#(id)","name":"\#(name)","input":\#(inputJSON)}],"usage":\#(usage)}}"#
    }

    private func toolResult(id: String, timestamp: String = "2026-07-31T10:01:05.000Z") -> String {
        #"{"type":"user","isSidechain":false,"timestamp":"\#(timestamp)","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"\#(id)","content":"ok"}]}}"#
    }

    // MARK: - Encoded project path (verified against real ~/.claude/projects entries)

    func testEncodedProjectPathMatchesRealEntries() {
        // Observed on disk: /Users/gdc/getspecstory -> -Users-gdc-getspecstory
        XCTAssertEqual(
            NativeSessionInspector.encodedProjectPath("/Users/gdc/getspecstory"),
            "-Users-gdc-getspecstory"
        )
        // Observed on disk: dots also become dashes, so a dot directory
        // yields a double dash.
        XCTAssertEqual(
            NativeSessionInspector.encodedProjectPath("/private/tmp/claude-501/.tmp13dSMz/plain-workspace"),
            "-private-tmp-claude-501--tmp13dSMz-plain-workspace"
        )
        // Trailing slashes never appear in an encoded name.
        XCTAssertEqual(
            NativeSessionInspector.encodedProjectPath("/Users/dev/proj/"),
            "-Users-dev-proj"
        )
    }

    // MARK: - Header facts

    func testHeaderCarriesCwdAndBranch() throws {
        let url = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"swift test"}"#),
            toolResult(id: "t1"),
        ])
        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        XCTAssertEqual(insight.cwd, "/Users/dev/proj")
        XCTAssertEqual(insight.branch, "feature/x")
        XCTAssertEqual(insight.model, "claude-opus-4-7")
    }

    // MARK: - Touched files and activity

    func testTouchedFilesDedupedNewestFirstAndCwdStripped() throws {
        let url = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Edit", inputJSON: #"{"file_path":"/Users/dev/proj/Sources/A.swift","old_string":"a","new_string":"b"}"#),
            toolResult(id: "t1"),
            assistantToolUse(id: "t2", name: "Write", inputJSON: #"{"file_path":"/Users/dev/proj/B.md","content":"hi"}"#),
            toolResult(id: "t2"),
            assistantToolUse(id: "t3", name: "Edit", inputJSON: #"{"file_path":"/Users/dev/proj/Sources/A.swift","old_string":"b","new_string":"c"}"#),
            toolResult(id: "t3"),
            assistantToolUse(id: "t4", name: "NotebookEdit", inputJSON: #"{"notebook_path":"/elsewhere/n.ipynb","new_source":"x"}"#),
            toolResult(id: "t4"),
        ])
        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        // Last mentioned first, deduped, cwd prefix stripped for display,
        // paths outside cwd left absolute.
        XCTAssertEqual(insight.touchedFiles, ["/elsewhere/n.ipynb", "Sources/A.swift", "B.md"])
    }

    func testActivityLinesForToolUseShapes() throws {
        let bash = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"swift test --filter NativeSessionInspectorTests And Much More Text Beyond Fifty Chars"}"#),
        ], sessionID: "bash-case")
        let bashInsight = NativeSessionInspector.insight(fromClaudeFile: bash, projectPath: nil)
        XCTAssertEqual(bashInsight.lastActivity, "Running Bash: " + String("swift test --filter NativeSessionInspectorTests And Much More Text Beyond Fifty Chars".prefix(50)))

        let task = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Task", inputJSON: #"{"description":"Explore the codebase","prompt":"go"}"#),
        ], sessionID: "task-case")
        XCTAssertEqual(NativeSessionInspector.insight(fromClaudeFile: task, projectPath: nil).lastActivity, "Running Task: Explore the codebase")

        let edit = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Edit", inputJSON: #"{"file_path":"/Users/dev/proj/Sources/A.swift"}"#),
        ], sessionID: "edit-case")
        XCTAssertEqual(NativeSessionInspector.insight(fromClaudeFile: edit, projectPath: nil).lastActivity, "Running Edit: A.swift")

        let text = try writeClaudeFixture([
            headerLine,
            #"{"type":"assistant","isSidechain":false,"timestamp":"2026-07-31T10:02:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"Here is the plan."}]}}"#,
        ], sessionID: "text-case")
        XCTAssertEqual(NativeSessionInspector.insight(fromClaudeFile: text, projectPath: nil).lastActivity, "Writing a response")

        let results = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"ls"}"#),
            toolResult(id: "t1"),
        ], sessionID: "result-case")
        XCTAssertEqual(NativeSessionInspector.insight(fromClaudeFile: results, projectPath: nil).lastActivity, "Processing tool results")

        let prompt = try writeClaudeFixture([
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"ls"}"#),
            toolResult(id: "t1"),
            headerLine,
        ], sessionID: "prompt-case")
        XCTAssertEqual(NativeSessionInspector.insight(fromClaudeFile: prompt, projectPath: nil).lastActivity, "Waiting at the prompt")

        let summary = try writeClaudeFixture([
            headerLine,
            #"{"type":"summary","summary":"Fix parser nesting","leafUuid":"x"}"#,
        ], sessionID: "summary-case")
        XCTAssertEqual(NativeSessionInspector.insight(fromClaudeFile: summary, projectPath: nil).lastActivity, "Waiting at the prompt")
    }

    // MARK: - Waiting for permission

    func testUnmatchedToolUseAtEOFMeansWaiting() throws {
        let url = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"rm -rf build"}"#),
        ])
        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        XCTAssertTrue(insight.waitingForPermission)
        XCTAssertEqual(insight.lastActivity, "Running Bash: rm -rf build")
    }

    func testMatchedToolUseIsNotWaiting() throws {
        let url = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"rm -rf build"}"#),
            toolResult(id: "t1"),
        ])
        XCTAssertFalse(NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil).waitingForPermission)
    }

    func testUserPromptAfterToolUseClearsWaiting() throws {
        let url = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"rm -rf build"}"#),
            headerLine,
        ])
        XCTAssertFalse(NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil).waitingForPermission)
    }

    // MARK: - Tokens

    func testTokenSumsAcrossAssistantTailLines() throws {
        let url = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"ls"}"#, usage: #"{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30}"#),
            toolResult(id: "t1"),
            assistantToolUse(id: "t2", name: "Bash", inputJSON: #"{"command":"pwd"}"#, usage: #"{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3}"#),
            toolResult(id: "t2"),
        ])
        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        XCTAssertEqual(insight.inputTokens, 11)
        XCTAssertEqual(insight.outputTokens, 22)
        XCTAssertEqual(insight.cacheReadTokens, 33)
    }

    func testNoUsageMeansNilTokens() throws {
        let url = try writeClaudeFixture([headerLine])
        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        XCTAssertNil(insight.inputTokens)
        XCTAssertNil(insight.outputTokens)
        XCTAssertNil(insight.cacheReadTokens)
    }

    // MARK: - Sidechain lines

    func testSidechainLinesIgnoredForActivityAndWaiting() throws {
        let url = try writeClaudeFixture([
            headerLine,
            #"{"type":"assistant","isSidechain":false,"timestamp":"2026-07-31T10:02:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"Done."}]}}"#,
            assistantToolUse(id: "side1", name: "Bash", inputJSON: #"{"command":"grep -r foo"}"#, sidechain: true),
        ])
        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        XCTAssertEqual(insight.lastActivity, "Writing a response")
        XCTAssertFalse(insight.waitingForPermission)
    }

    // MARK: - Timestamps

    func testLastEventAtParsesNewestTimestamp() throws {
        let url = try writeClaudeFixture([
            headerLine,
            assistantToolUse(id: "t1", name: "Bash", inputJSON: #"{"command":"ls"}"#, timestamp: "2026-07-31T11:22:33.000Z"),
        ])
        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        XCTAssertEqual(insight.lastEventAt, NativeSessionInspector.parseISODate("2026-07-31T11:22:33.000Z"))
        XCTAssertNotNil(insight.lastEventAt)
    }

    // MARK: - Huge file: header and tail windows both read

    func testHugeFileReadsHeaderFactsAndBoundsTailWindow() throws {
        var lines: [String] = [headerLine]
        // An edit that must fall OUTSIDE the 256 KB tail window.
        lines.append(assistantToolUse(id: "old1", name: "Edit", inputJSON: #"{"file_path":"/Users/dev/proj/old.swift"}"#))
        lines.append(toolResult(id: "old1"))
        // ~330 KB of filler user lines carrying no cwd or gitBranch keys.
        let fillerText = String(repeating: "x", count: 1024)
        for index in 0..<330 {
            lines.append(#"{"type":"user","isSidechain":false,"message":{"role":"user","content":"\#(fillerText)\#(index)"}}"#)
        }
        lines.append(assistantToolUse(id: "new1", name: "Edit", inputJSON: #"{"file_path":"/Users/dev/proj/new.swift"}"#))
        lines.append(toolResult(id: "new1"))

        let url = try writeClaudeFixture(lines, sessionID: "huge-case")
        let size = try XCTUnwrap(FileManager.default.attributesOfItem(atPath: url.path)[.size] as? Int)
        XCTAssertGreaterThan(size, 256 * 1024, "fixture must exceed the tail window")

        let insight = NativeSessionInspector.insight(fromClaudeFile: url, projectPath: nil)
        // Header window facts survive even though the tail window starts far
        // past them.
        XCTAssertEqual(insight.cwd, "/Users/dev/proj")
        XCTAssertEqual(insight.branch, "feature/x")
        // Tail window facts: the new edit is seen, the old edit is out of
        // range.
        XCTAssertEqual(insight.touchedFiles, ["new.swift"])
        XCTAssertFalse(insight.touchedFiles.contains("old.swift"))
        XCTAssertEqual(insight.lastActivity, "Processing tool results")
    }

    // MARK: - Locating files

    func testInspectClaudeLocatesViaEncodedProjectPath() throws {
        try writeClaudeFixture([headerLine], sessionID: "locate-1", projectDir: "-Users-dev-proj")
        let insight = NativeSessionInspector.inspectClaude(
            sessionID: "locate-1",
            projectPath: "/Users/dev/proj",
            projectsRoot: projectsRoot
        )
        XCTAssertEqual(insight?.cwd, "/Users/dev/proj")
        XCTAssertEqual(insight?.branch, "feature/x")
    }

    func testInspectClaudeFallsBackToScanWithoutProjectPath() throws {
        try writeClaudeFixture([headerLine], sessionID: "locate-2", projectDir: "-Users-dev-proj")
        let insight = NativeSessionInspector.inspectClaude(
            sessionID: "locate-2",
            projectPath: nil,
            projectsRoot: projectsRoot
        )
        XCTAssertEqual(insight?.branch, "feature/x")
    }

    func testInspectClaudeMissingFileFallsBackToProjectPath() throws {
        let insight = NativeSessionInspector.inspectClaude(
            sessionID: "nope",
            projectPath: "/Users/dev/proj",
            projectsRoot: projectsRoot
        )
        XCTAssertEqual(insight?.cwd, "/Users/dev/proj")
        XCTAssertNil(insight?.branch)
        XCTAssertNil(NativeSessionInspector.inspectClaude(sessionID: "nope", projectPath: nil, projectsRoot: tempDir.appendingPathComponent("does-not-exist")))
    }

    // MARK: - Codex

    func testCodexLocateAndParse() throws {
        let sessionID = "019e9552-4096-77f0-9150-4de1ac14aabe"
        let dayDir = tempDir.appendingPathComponent("codex/2026/07/31")
        try FileManager.default.createDirectory(at: dayDir, withIntermediateDirectories: true)
        let url = dayDir.appendingPathComponent("rollout-2026-07-31T10-00-00-\(sessionID).jsonl")
        let lines = [
            #"{"timestamp":"2026-07-31T10:00:00.000Z","type":"session_meta","payload":{"id":"\#(sessionID)","cwd":"/Users/dev/codexproj","git":{"branch":"main"}}}"#,
            #"{"timestamp":"2026-07-31T10:00:01.000Z","type":"turn_context","payload":{"cwd":"/Users/dev/codexproj","model":"gpt-5.5"}}"#,
            #"{"timestamp":"2026-07-31T10:00:30.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":50,"cached_input_tokens":25}}}}"#,
            #"{"timestamp":"2026-07-31T10:05:00.000Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"done"}}"#,
        ]
        try (lines.joined(separator: "\n") + "\n").write(to: url, atomically: true, encoding: .utf8)

        let root = tempDir.appendingPathComponent("codex")
        let insight = NativeSessionInspector.inspectCodex(sessionID: sessionID, projectPath: nil, sessionsRoot: root)
        XCTAssertEqual(insight?.cwd, "/Users/dev/codexproj")
        XCTAssertEqual(insight?.branch, "main")
        XCTAssertEqual(insight?.model, "gpt-5.5")
        XCTAssertEqual(insight?.lastActivity, "Task complete")
        XCTAssertEqual(insight?.inputTokens, 100)
        XCTAssertEqual(insight?.outputTokens, 50)
        XCTAssertEqual(insight?.cacheReadTokens, 25)
        XCTAssertEqual(insight?.lastEventAt, NativeSessionInspector.parseISODate("2026-07-31T10:05:00.000Z"))
        XCTAssertEqual(insight?.waitingForPermission, false)
    }

    func testCodexMissingSessionFallsBackToProjectPath() {
        let root = tempDir.appendingPathComponent("codex-empty")
        XCTAssertNil(NativeSessionInspector.inspectCodex(sessionID: "missing", projectPath: nil, sessionsRoot: root))
        let insight = NativeSessionInspector.inspectCodex(sessionID: "missing", projectPath: "/Users/dev/p", sessionsRoot: root)
        XCTAssertEqual(insight?.cwd, "/Users/dev/p")
    }

    // MARK: - Other providers

    func testOtherProvidersSurfaceProjectPathOnly() {
        let insight = NativeSessionInspector.inspect(provider: "cursor", sessionID: "s1", projectPath: "/Users/dev/p")
        XCTAssertEqual(insight?.cwd, "/Users/dev/p")
        XCTAssertNil(insight?.branch)
        XCTAssertEqual(insight?.touchedFiles, [])
        XCTAssertNil(NativeSessionInspector.inspect(provider: "cursor", sessionID: "s1", projectPath: nil))
    }
}
