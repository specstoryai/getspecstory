import XCTest

@testable import SpecStoryKit

/// The supervisor always spawns `<binary> watch --json
/// --no-version-check`. With /bin/sh as the binary, sh reads the file named
/// "watch" in the child's working directory (the project path) as its command
/// file, so each fake project carries its own fake-CLI body. No fixture is
/// ever exec'd directly (see EngineFixtures.shell for why).
final class WatchSupervisorTests: XCTestCase {
    private var fixtureDir: URL!
    private var spawnLog: URL!
    private var supervisors: [WatchSupervisor] = []

    private let longRunningBody = """
        pwd >> "$SPAWN_LOG"
        exec sleep 30
        """

    override func setUpWithError() throws {
        fixtureDir = try EngineFixtures.makeTempDirectory("supervisor")
        spawnLog = fixtureDir.appendingPathComponent("spawns.log")
        FileManager.default.createFile(atPath: spawnLog.path, contents: Data())
    }

    override func tearDownWithError() throws {
        // Reap every child promptly so no sleep fixture outlives the test.
        for supervisor in supervisors { supervisor.stopAll(patience: 0.2) }
        supervisors.removeAll()
        usleep(300_000)
        try? FileManager.default.removeItem(at: fixtureDir)
    }

    private func makeSupervisor(cap: Int) -> WatchSupervisor {
        let logPath = spawnLog.path
        let supervisor = WatchSupervisor(binary: EngineFixtures.shell, maxChildren: cap) {
            ["SPAWN_LOG": logPath]
        }
        supervisor.evictionPatience = 1
        supervisors.append(supervisor)
        return supervisor
    }

    private func makeProject(_ name: String, body: String? = nil) throws -> String {
        let dir = fixtureDir.appendingPathComponent("proj-\(name)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        _ = try EngineFixtures.makeBody(body ?? longRunningBody, in: dir, name: "watch")
        return dir.path
    }

    private func spawnCounts() -> [String: Int] {
        guard let content = try? String(contentsOf: spawnLog, encoding: .utf8) else { return [:] }
        var counts: [String: Int] = [:]
        for line in content.split(separator: "\n") {
            counts[String(line), default: 0] += 1
        }
        return counts
    }

    func testSetProjectsReconcilesKeepingExistingChildren() throws {
        let supervisor = makeSupervisor(cap: 4)
        let a = try makeProject("a")
        let b = try makeProject("b")
        let c = try makeProject("c")

        supervisor.setProjects([a, b])
        EngineFixtures.waitUntil(message: "a and b did not spawn") {
            self.spawnCounts()[a] == 1 && self.spawnCounts()[b] == 1
        }
        XCTAssertEqual(Set(supervisor.watchedProjects), Set([a, b]))

        supervisor.setProjects([b, c])
        EngineFixtures.waitUntil(message: "c did not spawn") { self.spawnCounts()[c] == 1 }
        XCTAssertEqual(Set(supervisor.watchedProjects), Set([b, c]))
        // b was kept, not respawned; a was evicted, not respawned.
        usleep(200_000)
        XCTAssertEqual(spawnCounts()[b], 1)
        XCTAssertEqual(spawnCounts()[a], 1)
    }

    func testSetProjectsHonorsCap() throws {
        let supervisor = makeSupervisor(cap: 2)
        let a = try makeProject("a")
        let b = try makeProject("b")
        let c = try makeProject("c")

        supervisor.setProjects([a, b, c])
        EngineFixtures.waitUntil(message: "a and b did not spawn") {
            self.spawnCounts()[a] == 1 && self.spawnCounts()[b] == 1
        }
        XCTAssertEqual(Set(supervisor.watchedProjects), Set([a, b]))
        usleep(200_000)
        XCTAssertNil(spawnCounts()[c], "the path beyond the cap must never spawn")
    }

    func testEnsureWatchingPromotesAndEvictsLRU() throws {
        let supervisor = makeSupervisor(cap: 2)
        let a = try makeProject("a")
        let b = try makeProject("b")
        let c = try makeProject("c")

        supervisor.ensureWatching(a)
        usleep(50_000)
        supervisor.ensureWatching(b)
        EngineFixtures.waitUntil(message: "a and b did not spawn") {
            self.spawnCounts()[a] == 1 && self.spawnCounts()[b] == 1
        }
        // Promote a; b becomes least recently active.
        usleep(50_000)
        supervisor.ensureWatching(a)
        usleep(50_000)
        supervisor.ensureWatching(c)
        EngineFixtures.waitUntil(message: "c did not spawn") { self.spawnCounts()[c] == 1 }
        XCTAssertEqual(Set(supervisor.watchedProjects), Set([a, c]))
        XCTAssertEqual(spawnCounts()[a], 1, "promotion must not respawn")
    }

    func testRestartAllCoalescesByGeneration() throws {
        let supervisor = makeSupervisor(cap: 2)
        let a = try makeProject("a")

        supervisor.setProjects([a])
        EngineFixtures.waitUntil(message: "a did not spawn") { self.spawnCounts()[a] == 1 }

        // Two quick restarts must produce exactly one respawn (latest wins).
        supervisor.restartAll()
        supervisor.restartAll()
        EngineFixtures.waitUntil(message: "a did not respawn") { self.spawnCounts()[a] == 2 }
        usleep(500_000)
        XCTAssertEqual(spawnCounts()[a], 2)
        XCTAssertEqual(supervisor.watchedProjects, [a])
    }

    func testUnexpectedExitRestartsOnceThenMarksErrored() throws {
        let supervisor = makeSupervisor(cap: 2)
        supervisor.restartDelay = 0.15
        // This fake child dies immediately, simulating a crashing watch.
        let a = try makeProject(
            "a",
            body: """
                pwd >> "$SPAWN_LOG"
                exit 7
                """)

        let logLock = NSLock()
        var errorLogs: [String] = []
        supervisor.onLog = { path, level, message in
            guard level == "error", path == a else { return }
            logLock.lock()
            errorLogs.append(message)
            logLock.unlock()
        }

        supervisor.setProjects([a])
        EngineFixtures.waitUntil(message: "no auto-restart happened") { self.spawnCounts()[a] == 2 }
        EngineFixtures.waitUntil(message: "no errored log arrived") {
            logLock.lock()
            defer { logLock.unlock() }
            return !errorLogs.isEmpty
        }
        // The second unexpected exit within the window must not restart again.
        usleep(400_000)
        XCTAssertEqual(spawnCounts()[a], 2)
        XCTAssertTrue(supervisor.watchedProjects.isEmpty)
    }

    func testStopAllClearsFleet() throws {
        let supervisor = makeSupervisor(cap: 2)
        let a = try makeProject("a")
        supervisor.setProjects([a])
        EngineFixtures.waitUntil(message: "a did not spawn") { self.spawnCounts()[a] == 1 }

        supervisor.stopAll(patience: 0.5)
        EngineFixtures.waitUntil(message: "fleet did not clear") { supervisor.watchedProjects.isEmpty }
        usleep(300_000)
        XCTAssertEqual(spawnCounts()[a], 1, "stopAll must not respawn")
    }

    func testWatchEventsAreForwardedWithProjectPath() throws {
        let supervisor = makeSupervisor(cap: 2)
        let a = try makeProject(
            "a",
            body: """
                pwd >> "$SPAWN_LOG"
                echo '{"timestamp":"2026-07-31T00:00:00Z","action":"created","session_id":"s1","provider":"claude"}'
                exec sleep 30
                """)

        let eventLock = NSLock()
        var received: [(String, WatchEvent)] = []
        supervisor.onEvent = { path, event in
            eventLock.lock()
            received.append((path, event))
            eventLock.unlock()
        }
        supervisor.setProjects([a])
        EngineFixtures.waitUntil(message: "no watch event arrived") {
            eventLock.lock()
            defer { eventLock.unlock() }
            return !received.isEmpty
        }
        eventLock.lock()
        let first = received.first
        eventLock.unlock()
        XCTAssertEqual(first?.0, a)
        XCTAssertEqual(first?.1.sessionID, "s1")
    }
}
