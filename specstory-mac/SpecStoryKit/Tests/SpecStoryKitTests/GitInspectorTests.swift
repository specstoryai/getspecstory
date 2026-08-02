import XCTest
@testable import SpecStoryKit

final class GitInspectorTests: XCTestCase {
    private var repoDir: URL!

    override func setUpWithError() throws {
        guard FileManager.default.isExecutableFile(atPath: "/usr/bin/git"),
              GitInspector.runGit(["--version"], at: NSTemporaryDirectory()) != nil else {
            throw XCTSkip("git is unavailable on this machine")
        }
        repoDir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("gitinspector-tests-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: repoDir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        if let repoDir {
            try? FileManager.default.removeItem(at: repoDir.deletingLastPathComponent().appendingPathComponent(repoDir.lastPathComponent + "-wt"))
            try? FileManager.default.removeItem(at: repoDir)
        }
    }

    private func git(_ arguments: [String]) throws {
        let result = GitInspector.runGit(arguments, at: repoDir.path)
        guard let result, result.exitCode == 0 else {
            throw XCTSkip("git \(arguments.joined(separator: " ")) failed; skipping")
        }
    }

    private func write(_ name: String, _ content: String) throws {
        try content.write(to: repoDir.appendingPathComponent(name), atomically: true, encoding: .utf8)
    }

    private func makeRepoWithCommit() throws {
        try git(["init"])
        try git(["symbolic-ref", "HEAD", "refs/heads/main"])
        try git(["config", "user.email", "test@example.com"])
        try git(["config", "user.name", "Test"])
        try git(["config", "commit.gpgsign", "false"])
        try write("a.txt", (1...10).map { "line \($0)" }.joined(separator: "\n") + "\n")
        try git(["add", "-A"])
        try git(["commit", "-m", "init"])
    }

    // MARK: - state

    func testStateOnNonRepoIsNil() throws {
        XCTAssertNil(GitInspector.state(at: repoDir.path))
        XCTAssertNil(GitInspector.state(at: "/definitely/not/a/real/path"))
    }

    func testStateReportsBranchDirtyFilesAndShortstat() throws {
        try makeRepoWithCommit()
        // Modify tracked lines (insertions and deletions) plus one untracked
        // file.
        try write("a.txt", (1...10).map { $0 <= 3 ? "changed \($0)" : "line \($0)" }.joined(separator: "\n") + "\n")
        try write("b.txt", "new file\n")

        let state = try XCTUnwrap(GitInspector.state(at: repoDir.path))
        XCTAssertEqual(state.branch, "main")
        XCTAssertFalse(state.isWorktree)

        let byPath = Dictionary(uniqueKeysWithValues: state.dirtyFiles.map { ($0.path, $0.status) })
        XCTAssertEqual(byPath["a.txt"], "M")
        XCTAssertEqual(byPath["b.txt"], "??")

        let insertions = try XCTUnwrap(state.insertions)
        let deletions = try XCTUnwrap(state.deletions)
        XCTAssertEqual(insertions, 3)
        XCTAssertEqual(deletions, 3)
    }

    func testCleanRepoHasNoDirtyFilesAndNilShortstat() throws {
        try makeRepoWithCommit()
        let state = try XCTUnwrap(GitInspector.state(at: repoDir.path))
        XCTAssertEqual(state.dirtyFiles, [])
        XCTAssertNil(state.insertions)
        XCTAssertNil(state.deletions)
    }

    func testWorktreeDetection() throws {
        try makeRepoWithCommit()
        let worktreePath = repoDir.deletingLastPathComponent()
            .appendingPathComponent(repoDir.lastPathComponent + "-wt").path
        try git(["worktree", "add", "-b", "wt-branch", worktreePath])

        let state = try XCTUnwrap(GitInspector.state(at: worktreePath))
        XCTAssertTrue(state.isWorktree)
        XCTAssertEqual(state.branch, "wt-branch")
    }

    // MARK: - diff

    func testDiffForSingleFile() throws {
        try makeRepoWithCommit()
        try write("a.txt", "totally new\n")
        let diff = try XCTUnwrap(GitInspector.diff(at: repoDir.path, file: "a.txt"))
        XCTAssertTrue(diff.contains("a.txt"))
        XCTAssertTrue(diff.contains("+totally new"))
        XCTAssertFalse(diff.contains("... diff truncated ..."))
    }

    func testDiffCapsAtFourHundredLinesWithMarker() throws {
        try makeRepoWithCommit()
        try write("big.txt", (1...600).map { "original \($0)" }.joined(separator: "\n") + "\n")
        try git(["add", "-A"])
        try git(["commit", "-m", "big file"])
        try write("big.txt", (1...600).map { "rewritten \($0)" }.joined(separator: "\n") + "\n")

        let diff = try XCTUnwrap(GitInspector.diff(at: repoDir.path, file: "big.txt"))
        let lines = diff.components(separatedBy: "\n")
        XCTAssertEqual(lines.count, GitInspector.diffLineCap + 1)
        XCTAssertEqual(lines.last, "... diff truncated ...")
    }

    func testFullDiffWhenFileIsNil() throws {
        try makeRepoWithCommit()
        try write("a.txt", "changed everything\n")
        let diff = try XCTUnwrap(GitInspector.diff(at: repoDir.path, file: nil))
        XCTAssertTrue(diff.contains("a.txt"))
    }

    func testDiffOnNonRepoIsNil() throws {
        XCTAssertNil(GitInspector.diff(at: repoDir.path, file: nil))
    }
}
