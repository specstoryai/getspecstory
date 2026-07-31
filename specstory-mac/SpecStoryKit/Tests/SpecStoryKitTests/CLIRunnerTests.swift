import XCTest

@testable import SpecStoryKit

final class CLIRunnerTests: XCTestCase {
    private var fixtureDir: URL!

    override func setUpWithError() throws {
        fixtureDir = try EngineFixtures.makeTempDirectory("cli-runner")
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: fixtureDir)
    }

    /// The child is /bin/sh with the body script as its command file, so the
    /// test arguments land in the script's "$@" unchanged.
    private func makeRunner(
        body: String, arguments: [String] = [], extraEnvironment: [String: String] = [:]
    ) throws -> CLIRunner {
        let bodyURL = try EngineFixtures.makeBody(body, in: fixtureDir)
        return CLIRunner(
            binary: EngineFixtures.shell,
            arguments: [bodyURL.path] + arguments,
            workingDirectory: fixtureDir.path,
            environment: extraEnvironment)
    }

    private func collectEvents(from runner: CLIRunner, timeout: TimeInterval = 5) async -> [CLIRunnerEvent] {
        await withTaskGroup(of: [CLIRunnerEvent]?.self) { group in
            group.addTask {
                var events: [CLIRunnerEvent] = []
                for await event in runner.events { events.append(event) }
                return events
            }
            group.addTask {
                try? await Task.sleep(nanoseconds: UInt64(timeout * 1_000_000_000))
                return nil
            }
            let winner = await group.next() ?? nil
            group.cancelAll()
            if winner == nil { XCTFail("event stream did not finish within \(timeout)s") }
            return winner ?? []
        }
    }

    func testStreamingEventsClassifyStdoutStderrAndTermination() async throws {
        let runner = try makeRunner(
            body: """
                echo '{"timestamp":"2026-07-31T00:00:00Z","action":"created","session_id":"abc","provider":"claude"}'
                echo 'time=2026-07-31T00:00:00Z level=WARN msg="cloud retry" attempt=2'
                echo 'plain line'
                echo 'stderr line' >&2
                exit 5
                """)
        try runner.launch()
        let events = await collectEvents(from: runner)

        var sawWatchEvent = false
        var sawWarn = false
        var sawPlain = false
        var sawStderr = false
        var exitCode: Int32?
        for event in events {
            switch event {
            case .watchEvent(let watchEvent):
                sawWatchEvent = true
                XCTAssertEqual(watchEvent.sessionID, "abc")
                XCTAssertEqual(watchEvent.action, .created)
            case .log(let level, let message):
                if level == "warn", message == "cloud retry attempt=2" { sawWarn = true }
                if level == "info", message == "plain line" { sawPlain = true }
                if level == "error", message == "stderr line" { sawStderr = true }
            case .terminated(let code):
                exitCode = code
            }
        }
        XCTAssertTrue(sawWatchEvent)
        XCTAssertTrue(sawWarn)
        XCTAssertTrue(sawPlain)
        XCTAssertTrue(sawStderr)
        XCTAssertEqual(exitCode, 5)
        if case .terminated = events.last {} else {
            XCTFail("terminated must be the final event, got \(String(describing: events.last))")
        }
        XCTAssertFalse(runner.isRunning)
    }

    func testNoVersionCheckIsAlwaysAppended() async throws {
        let runner = try makeRunner(body: "echo \"$@\"", arguments: ["watch", "--json"])
        try runner.launch()
        let events = await collectEvents(from: runner)
        let messages = events.compactMap { event -> String? in
            if case let .log(_, message) = event { return message }
            return nil
        }
        XCTAssertEqual(messages, ["watch --json --no-version-check"])
    }

    func testEnvironmentMergesProvidedEntries() async throws {
        let runner = try makeRunner(
            body: "echo \"token=$SPECSTORY_CLOUD_TOKEN home=$HOME\"",
            extraEnvironment: ["SPECSTORY_CLOUD_TOKEN": "sekrit"])
        try runner.launch()
        let events = await collectEvents(from: runner)
        let messages = events.compactMap { event -> String? in
            if case let .log(_, message) = event { return message }
            return nil
        }
        XCTAssertEqual(messages.count, 1)
        XCTAssertTrue(messages[0].contains("token=sekrit"))
        // Inherited environment still present.
        XCTAssertFalse(messages[0].hasSuffix("home="))
    }

    func testOneShotRunStripsSlogLines() async throws {
        let bodyURL = try EngineFixtures.makeBody(
            """
            echo 'time=2026-07-31T00:00:00Z level=INFO msg=indexing'
            echo '[{"sessionId":"s1"}]'
            """, in: fixtureDir)
        let stdout = try await CLIRunner.run(
            binary: EngineFixtures.shell, arguments: [bodyURL.path, "list", "--json"],
            workingDirectory: fixtureDir.path, environment: [:], timeout: 5)
        XCTAssertEqual(stdout.trimmingCharacters(in: .whitespacesAndNewlines), "[{\"sessionId\":\"s1\"}]")
    }

    func testOneShotRunDetailedReportsExitCode() async throws {
        let result = try await CLIRunner.runDetailed(
            binary: URL(fileURLWithPath: "/usr/bin/false"), arguments: [],
            workingDirectory: fixtureDir.path, environment: [:], timeout: 5)
        XCTAssertEqual(result.exitCode, 1)
        XCTAssertEqual(result.stdout, "")
    }

    func testOneShotRunTimesOutAndKills() async throws {
        let bodyURL = try EngineFixtures.makeBody("exec sleep 30", in: fixtureDir)
        let started = Date()
        do {
            _ = try await CLIRunner.run(
                binary: EngineFixtures.shell, arguments: [bodyURL.path],
                workingDirectory: fixtureDir.path, environment: [:], timeout: 0.4)
            XCTFail("expected a timeout")
        } catch let error as CLIRunnerError {
            guard case .timeout = error else { return XCTFail("unexpected error: \(error)") }
        }
        XCTAssertLessThan(Date().timeIntervalSince(started), 2.0)
    }

    func testTerminateSIGTERMFinishesPromptlyWhenChildCooperates() async throws {
        let runner = try makeRunner(body: "exec sleep 30")
        try runner.launch()
        EngineFixtures.waitUntil(message: "child did not start") { runner.isRunning }
        let started = Date()
        runner.terminate(patience: 10)
        let events = await collectEvents(from: runner, timeout: 4)
        // Well under the 10 s patience: SIGTERM sufficed, no SIGKILL wait.
        XCTAssertLessThan(Date().timeIntervalSince(started), 2.0)
        guard case .terminated(let code)? = events.last else {
            return XCTFail("expected terminated, got \(String(describing: events.last))")
        }
        XCTAssertEqual(code, SIGTERM)
    }

    func testTerminateEscalatesToSIGKILLAfterPatience() throws {
        // The child ignores TERM (the handler is a no-op and the loop keeps
        // it alive), forcing the SIGKILL escalation path. The test must wait
        // for "ready" so the trap is installed before SIGTERM arrives.
        let runner = try makeRunner(
            body: """
                trap ':' TERM
                echo ready
                while :; do sleep 0.1; done
                """)
        let lock = NSLock()
        var events: [CLIRunnerEvent] = []
        var streamFinished = false
        Task {
            for await event in runner.events {
                lock.lock()
                events.append(event)
                lock.unlock()
            }
            lock.lock()
            streamFinished = true
            lock.unlock()
        }
        try runner.launch()
        EngineFixtures.waitUntil(message: "child never echoed ready") {
            lock.lock()
            defer { lock.unlock() }
            return events.contains { event in
                if case .log(_, let message) = event { return message == "ready" }
                return false
            }
        }
        let started = Date()
        runner.terminate(patience: 0.4)
        EngineFixtures.waitUntil(timeout: 4, message: "stream never finished") {
            lock.lock()
            defer { lock.unlock() }
            return streamFinished
        }
        let elapsed = Date().timeIntervalSince(started)
        XCTAssertGreaterThanOrEqual(elapsed, 0.4, "must not kill before patience elapses")
        XCTAssertLessThan(elapsed, 2.0)
        lock.lock()
        let last = events.last
        lock.unlock()
        guard case .terminated(let code)? = last else {
            return XCTFail("expected terminated, got \(String(describing: last))")
        }
        XCTAssertEqual(code, SIGKILL)
        XCTAssertFalse(runner.isRunning)
    }

    func testLaunchTwiceThrows() throws {
        let runner = try makeRunner(body: "exit 0")
        try runner.launch()
        XCTAssertThrowsError(try runner.launch())
    }

    func testLineBufferHoldsPartialLines() {
        var buffer = CLILineBuffer()
        XCTAssertEqual(buffer.append(Data("ab".utf8)), [])
        XCTAssertEqual(buffer.append(Data("c\nde".utf8)), ["abc"])
        XCTAssertEqual(buffer.append(Data("f\r\n\n".utf8)), ["def", ""])
        XCTAssertNil(buffer.flush())
        XCTAssertEqual(buffer.append(Data("tail".utf8)), [])
        XCTAssertEqual(buffer.flush(), "tail")
    }
}
