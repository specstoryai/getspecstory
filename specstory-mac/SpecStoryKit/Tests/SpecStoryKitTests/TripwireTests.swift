import XCTest

@testable import SpecStoryKit

final class ProjectDirEncodingTests: XCTestCase {
    func testEncodeMatchesClaudeConvention() {
        // Real examples from ~/.claude/projects: slashes, dots, and
        // underscores all become dashes; alphanumerics and dashes survive.
        XCTAssertEqual(ClaudeStyleProjectDirectory.encode("/private/tmp"), "-private-tmp")
        XCTAssertEqual(
            ClaudeStyleProjectDirectory.encode("/Users/g/my_repo.x"), "-Users-g-my-repo-x")
        XCTAssertEqual(
            ClaudeStyleProjectDirectory.encode("/Users/g/spec-story"), "-Users-g-spec-story")
    }

    func testRoundTripThroughRealDirectories() throws {
        let base = try EngineFixtures.makeTempDirectory("encoding")
        defer { try? FileManager.default.removeItem(at: base) }
        // Dashes, dots, and underscores in components exercise the lossy
        // cases that need filesystem-guided decoding.
        let deep = base
            .appendingPathComponent("enc.test")
            .appendingPathComponent("my-project_x")
        try FileManager.default.createDirectory(at: deep, withIntermediateDirectories: true)

        let encoded = ClaudeStyleProjectDirectory.encode(deep.path)
        XCTAssertEqual(ClaudeStyleProjectDirectory.decode(encoded), deep.path)
    }

    func testDecodeFallsBackToNaiveSlashesWhenPathIsGone() {
        XCTAssertEqual(
            ClaudeStyleProjectDirectory.decode("-nonexistent-fixture-abc"),
            "/nonexistent/fixture/abc")
    }

    func testDecodeRejectsNonEncodedNames() {
        XCTAssertNil(ClaudeStyleProjectDirectory.decode("not-encoded"))
        XCTAssertNil(ClaudeStyleProjectDirectory.decode("-"))
    }
}

final class SessionTripwireTests: XCTestCase {
    func testCreatedFileFiresActivityWithDecodedProjectHintAndDebounces() throws {
        let base = try EngineFixtures.makeTempDirectory("tripwire")
        defer { try? FileManager.default.removeItem(at: base) }

        // A real project directory the encoded name should decode back to.
        let project = base.appendingPathComponent("demo-project")
        try FileManager.default.createDirectory(at: project, withIntermediateDirectories: true)

        // A fake ~/.claude/projects root containing the encoded project dir.
        let root = base.appendingPathComponent("claude-projects")
        let encoded = ClaudeStyleProjectDirectory.encode(project.path)
        let sessionDir = root.appendingPathComponent(encoded)
        try FileManager.default.createDirectory(at: sessionDir, withIntermediateDirectories: true)

        let tripwire = SessionTripwire(
            roots: [ProviderRoot(provider: .claude, path: root.path)],
            latency: 0.15, debounce: 0.5, rescanInterval: 60)
        let lock = NSLock()
        var activities: [(Provider, String?)] = []
        let fired = expectation(description: "activity fired")
        fired.assertForOverFulfill = false
        tripwire.onActivity = { provider, hint in
            lock.lock()
            activities.append((provider, hint))
            lock.unlock()
            fired.fulfill()
        }
        tripwire.start()
        defer { tripwire.stop() }

        // Let the stream begin before creating events it should see.
        usleep(250_000)
        try Data("{}".utf8).write(to: sessionDir.appendingPathComponent("11111111.jsonl"))
        wait(for: [fired], timeout: 3)

        // A second file right away is debounced into silence.
        try Data("{}".utf8).write(to: sessionDir.appendingPathComponent("22222222.jsonl"))
        usleep(450_000)

        lock.lock()
        let snapshot = activities
        lock.unlock()
        XCTAssertEqual(snapshot.count, 1, "debounce must collapse rapid activity to one callback")
        let first = try XCTUnwrap(snapshot.first)
        XCTAssertEqual(first.0, .claude)
        XCTAssertEqual(first.1, project.path)
    }

    func testStartSkipsMissingRootsWithoutCrashing() throws {
        let tripwire = SessionTripwire(
            roots: [ProviderRoot(provider: .deepseek, path: "/nonexistent/deepseek-root")],
            latency: 0.1, debounce: 0.5, rescanInterval: 60)
        tripwire.start()
        tripwire.stop()
    }
}
