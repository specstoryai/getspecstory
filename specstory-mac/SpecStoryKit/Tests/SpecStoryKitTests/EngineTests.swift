import XCTest

@testable import SpecStoryKit

// Shared fixture helpers for the engine tests.
enum EngineFixtures {
    /// A fresh directory under the system temp dir, canonicalized with POSIX
    /// realpath so paths match what child processes see via getcwd (URL's
    /// resolvingSymlinksInPath strips /private and must not be used here).
    static func makeTempDirectory(_ label: String) throws -> URL {
        let dir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("specstory-engine-\(label)-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var buffer = [CChar](repeating: 0, count: Int(PATH_MAX))
        if realpath(dir.path, &buffer) != nil {
            return URL(fileURLWithPath: String(cString: buffer))
        }
        return dir
    }

    /// Writes an executable file (for tests that only need the exec bit).
    static func makeScript(_ body: String, in directory: URL, name: String = "fixture.sh") throws -> URL {
        let url = directory.appendingPathComponent(name)
        try ("#!/bin/sh\n" + body + "\n").write(to: url, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
        return url
    }

    /// Fixture children are always /bin/sh reading a body script passed as
    /// its command-file argument. Freshly written files must never be exec'd
    /// directly in tests: macOS assesses first-run executables (syspolicyd)
    /// and flakily stalls them for many seconds. /bin/sh is a platform
    /// binary, and the body script is just data it reads.
    static let shell = URL(fileURLWithPath: "/bin/sh")

    /// Writes a shell body script (plain data, not executable) and returns
    /// its path, for use as the first argument to `shell`.
    static func makeBody(_ body: String, in directory: URL, name: String = "fixture.sh") throws -> URL {
        let url = directory.appendingPathComponent(name)
        try (body + "\n").write(to: url, atomically: true, encoding: .utf8)
        return url
    }

    static func waitUntil(
        timeout: TimeInterval = 3, message: String = "condition not met",
        file: StaticString = #filePath, line: UInt = #line,
        _ condition: () -> Bool
    ) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return }
            usleep(20_000)
        }
        XCTFail("\(message) within \(timeout)s", file: file, line: line)
    }
}

final class BinaryLocatorTests: XCTestCase {
    // sha256("abc"), a published test vector.
    private let abcSHA256 = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

    private func writeFixture(binaryHash: String) throws -> (binary: URL, manifest: URL, dir: URL) {
        let dir = try EngineFixtures.makeTempDirectory("locator")
        let binary = dir.appendingPathComponent("specstory_darwin_arm64")
        try "abc".write(to: binary, atomically: true, encoding: .utf8)
        let manifest = dir.appendingPathComponent("manifest.json")
        let json = """
            {"cliVersion":"test","sha256":{"arm64":"\(binaryHash)","x86_64":"\(binaryHash)"}}
            """
        try json.write(to: manifest, atomically: true, encoding: .utf8)
        return (binary, manifest, dir)
    }

    func testSha256HexMatchesKnownVector() throws {
        let fixture = try writeFixture(binaryHash: abcSHA256)
        defer { try? FileManager.default.removeItem(at: fixture.dir) }
        XCTAssertEqual(try BinaryLocator.sha256Hex(of: fixture.binary), abcSHA256)
    }

    func testVerifyHappyPath() throws {
        let fixture = try writeFixture(binaryHash: abcSHA256)
        defer { try? FileManager.default.removeItem(at: fixture.dir) }
        XCTAssertNoThrow(
            try BinaryLocator.verify(binary: fixture.binary, manifestURL: fixture.manifest, archKey: "arm64"))
    }

    func testVerifyMismatchThrowsDescriptiveError() throws {
        let wrong = String(repeating: "0", count: 64)
        let fixture = try writeFixture(binaryHash: wrong)
        defer { try? FileManager.default.removeItem(at: fixture.dir) }
        XCTAssertThrowsError(
            try BinaryLocator.verify(binary: fixture.binary, manifestURL: fixture.manifest, archKey: "arm64")
        ) { error in
            guard case let BinaryLocatorError.checksumMismatch(binary, expected, actual) = error else {
                return XCTFail("unexpected error: \(error)")
            }
            XCTAssertEqual(binary, "specstory_darwin_arm64")
            XCTAssertEqual(expected, wrong)
            XCTAssertEqual(actual, abcSHA256)
            XCTAssertNotNil((error as? LocalizedError)?.errorDescription)
        }
    }

    func testVerifyMissingArchEntryThrows() throws {
        let fixture = try writeFixture(binaryHash: abcSHA256)
        defer { try? FileManager.default.removeItem(at: fixture.dir) }
        XCTAssertThrowsError(
            try BinaryLocator.verify(binary: fixture.binary, manifestURL: fixture.manifest, archKey: "riscv"))
    }

    func testLocateHonorsSpecstoryBinOverride() throws {
        let dir = try EngineFixtures.makeTempDirectory("override")
        defer { try? FileManager.default.removeItem(at: dir) }
        let script = try EngineFixtures.makeScript("exit 0", in: dir)
        setenv("SPECSTORY_BIN", script.path, 1)
        defer { unsetenv("SPECSTORY_BIN") }
        XCTAssertEqual(try BinaryLocator.locate().path, script.path)
    }

    func testLocateRejectsRelativeOverride() {
        setenv("SPECSTORY_BIN", "relative/specstory", 1)
        defer { unsetenv("SPECSTORY_BIN") }
        XCTAssertThrowsError(try BinaryLocator.locate()) { error in
            guard case BinaryLocatorError.overrideNotAbsolute = error else {
                return XCTFail("unexpected error: \(error)")
            }
        }
    }

    func testLocateRejectsMissingOverride() {
        setenv("SPECSTORY_BIN", "/nonexistent/specstory-binary", 1)
        defer { unsetenv("SPECSTORY_BIN") }
        XCTAssertThrowsError(try BinaryLocator.locate()) { error in
            guard case BinaryLocatorError.overrideNotExecutable = error else {
                return XCTFail("unexpected error: \(error)")
            }
        }
    }

    func testMachineArchKeyIsSupported() throws {
        let key = try BinaryLocator.machineArchKey()
        XCTAssertTrue(["arm64", "x86_64"].contains(key))
    }
}

final class ProviderRootsTests: XCTestCase {
    func testAllCoversEveryProviderExactlyOnce() {
        let roots = ProviderRoots.all()
        XCTAssertEqual(roots.count, Provider.allCases.count)
        XCTAssertEqual(Set(roots.map { $0.provider }), Set(Provider.allCases))
        for root in roots {
            XCTAssertTrue(root.path.hasPrefix("/"), "\(root.provider) root is not absolute: \(root.path)")
            XCTAssertFalse(root.path.contains("~"), "\(root.provider) root has an unexpanded tilde")
        }
    }

    func testWellKnownRootLocations() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let byProvider = Dictionary(uniqueKeysWithValues: ProviderRoots.all().map { ($0.provider, $0.path) })
        XCTAssertEqual(byProvider[.claude], home + "/.claude/projects")
        XCTAssertEqual(byProvider[.cursor], home + "/.cursor/chats")
        XCTAssertEqual(byProvider[.cursoride], home + "/Library/Application Support/Cursor/User/globalStorage")
        XCTAssertEqual(byProvider[.gemini], home + "/.gemini/tmp")
        XCTAssertEqual(byProvider[.antigravity], home + "/.gemini/antigravity-cli/brain")
        XCTAssertEqual(byProvider[.deepseek], home + "/.deepseek/sessions")
        XCTAssertEqual(byProvider[.droid], home + "/.factory/sessions")
    }

    func testCodexHonorsCodexHome() {
        setenv("CODEX_HOME", "/custom/codex-home", 1)
        defer { unsetenv("CODEX_HOME") }
        let codex = ProviderRoots.all().first { $0.provider == .codex }
        XCTAssertEqual(codex?.path, "/custom/codex-home/sessions")
    }

    func testCodexDefaultsWithoutCodexHome() {
        unsetenv("CODEX_HOME")
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let codex = ProviderRoots.all().first { $0.provider == .codex }
        XCTAssertEqual(codex?.path, home + "/.codex/sessions")
    }
}

final class SlogParsingTests: XCTestCase {
    func testQuotedMessageWithAttrsIsReLeveled() {
        let line = "time=2026-07-31T00:00:00Z level=WARN msg=\"cloud sync retry\" attempt=2"
        guard case let .log(level, message) = CLIRunner.stdoutEvent(for: line) else {
            return XCTFail("expected a log event")
        }
        XCTAssertEqual(level, "warn")
        XCTAssertEqual(message, "cloud sync retry attempt=2")
    }

    func testUnquotedMessage() {
        let parsed = CLIRunner.parseSlogLine("time=2026-07-31T00:00:00Z level=ERROR msg=boom")
        XCTAssertEqual(parsed?.level, "error")
        XCTAssertEqual(parsed?.message, "boom")
    }

    func testEscapedQuoteInsideMessage() {
        let parsed = CLIRunner.parseSlogLine(#"time=x level=INFO msg="say \"hi\" now""#)
        XCTAssertEqual(parsed?.message, #"say "hi" now"#)
    }

    func testNonSlogLineIsNil() {
        XCTAssertNil(CLIRunner.parseSlogLine("just some text"))
        XCTAssertNil(CLIRunner.parseSlogLine("timestamp=1 level=INFO msg=x"))
    }

    func testJSONLineBecomesWatchEvent() {
        let line = #"{"timestamp":"2026-07-31T00:00:00Z","action":"created","session_id":"abc","provider":"claude"}"#
        guard case let .watchEvent(event) = CLIRunner.stdoutEvent(for: line) else {
            return XCTFail("expected a watch event")
        }
        XCTAssertEqual(event.action, .created)
        XCTAssertEqual(event.sessionID, "abc")
    }

    func testMalformedJSONFallsBackToInfoLog() {
        guard case let .log(level, message) = CLIRunner.stdoutEvent(for: "{not json") else {
            return XCTFail("expected a log event")
        }
        XCTAssertEqual(level, "info")
        XCTAssertEqual(message, "{not json")
    }

    func testStderrPlainLineIsErrorLevel() {
        guard case let .log(level, _) = CLIRunner.stderrEvent(for: "oops") else {
            return XCTFail("expected a log event")
        }
        XCTAssertEqual(level, "error")
    }

    func testStripSlogLines() {
        let mixed = "time=x level=INFO msg=a\n[{\"sessionId\":\"s\"}]\ntime=y level=WARN msg=b"
        XCTAssertEqual(CLIRunner.stripSlogLines(mixed), "[{\"sessionId\":\"s\"}]")
    }
}
