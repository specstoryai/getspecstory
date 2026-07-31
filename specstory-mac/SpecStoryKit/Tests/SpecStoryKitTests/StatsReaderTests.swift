import XCTest
@testable import SpecStoryKit

final class StatsReaderTests: XCTestCase {
    private var projectDir: URL!
    private var specstoryDir: URL!

    override func setUpWithError() throws {
        projectDir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("StatsReaderTests-\(UUID().uuidString)")
        specstoryDir = projectDir.appendingPathComponent(".specstory")
        try FileManager.default.createDirectory(at: specstoryDir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: projectDir)
    }

    private func write(_ contents: String, to filename: String) throws {
        try contents.write(
            to: specstoryDir.appendingPathComponent(filename),
            atomically: true,
            encoding: .utf8
        )
    }

    // MARK: - statistics.json

    func testStatisticsParsesCLIShape() throws {
        // Shape produced by the CLI's pkg/session/statistics.go.
        try write("""
        {
          "sessions": {
            "0b6a5c3e-1111-4222-8333-444455556666": {
              "user_message_count": 12,
              "agent_message_count": 30,
              "start_timestamp": "2025-01-01T10:00:00Z",
              "end_timestamp": "2025-01-01T11:30:00Z",
              "markdown_size_bytes": 48213,
              "provider": "claude",
              "last_updated": "2025-01-01T11:31:00Z"
            },
            "minimal-session": {}
          }
        }
        """, to: "statistics.json")

        let stats = StatsReader.statistics(projectPath: projectDir.path)
        XCTAssertNotNil(stats)
        XCTAssertEqual(stats?.count, 2)

        let full = stats?["0b6a5c3e-1111-4222-8333-444455556666"]
        XCTAssertEqual(full?.userMessageCount, 12)
        XCTAssertEqual(full?.agentMessageCount, 30)
        XCTAssertEqual(full?.startTimestamp, "2025-01-01T10:00:00Z")
        XCTAssertEqual(full?.endTimestamp, "2025-01-01T11:30:00Z")
        XCTAssertEqual(full?.markdownSizeBytes, 48213)
        XCTAssertEqual(full?.provider, "claude")
        XCTAssertEqual(full?.lastUpdated, "2025-01-01T11:31:00Z")

        // An entry with no fields decodes with all-nil values instead of failing.
        let minimal = stats?["minimal-session"]
        XCTAssertNotNil(minimal)
        XCTAssertNil(minimal?.userMessageCount)
    }

    func testStatisticsMissingFileReturnsNil() {
        XCTAssertNil(StatsReader.statistics(projectPath: projectDir.path))
    }

    func testStatisticsCorruptFileReturnsNil() throws {
        try write("{ not valid json !!", to: "statistics.json")
        XCTAssertNil(StatsReader.statistics(projectPath: projectDir.path))
    }

    func testStatisticsMissingProjectDirReturnsNil() {
        XCTAssertNil(StatsReader.statistics(projectPath: "/nonexistent/path/nowhere"))
    }

    // MARK: - .project.json

    func testProjectInfoParsesCLIShape() throws {
        // Shape produced by the CLI's pkg/utils/project_identity.go.
        try write("""
        {
          "workspace_id": "aaaa-bbbb-cccc-dddd",
          "workspace_id_at": "2025-01-01T00:00:00Z",
          "git_id": "1111-2222-3333-4444",
          "git_id_at": "2025-01-02T00:00:00Z",
          "project_name": "getspecstory"
        }
        """, to: ".project.json")

        let info = StatsReader.projectInfo(projectPath: projectDir.path)
        XCTAssertEqual(info?.workspaceID, "aaaa-bbbb-cccc-dddd")
        XCTAssertEqual(info?.workspaceIDAt, "2025-01-01T00:00:00Z")
        XCTAssertEqual(info?.gitID, "1111-2222-3333-4444")
        XCTAssertEqual(info?.gitIDAt, "2025-01-02T00:00:00Z")
        XCTAssertEqual(info?.projectName, "getspecstory")
    }

    func testProjectInfoWithoutGitID() throws {
        // Remote-less projects have only the workspace identity.
        try write("""
        {
          "workspace_id": "aaaa-bbbb-cccc-dddd",
          "workspace_id_at": "2025-01-01T00:00:00Z"
        }
        """, to: ".project.json")

        let info = StatsReader.projectInfo(projectPath: projectDir.path)
        XCTAssertEqual(info?.workspaceID, "aaaa-bbbb-cccc-dddd")
        XCTAssertNil(info?.gitID)
        XCTAssertNil(info?.gitIDAt)
    }

    func testProjectInfoMissingFileReturnsNil() {
        XCTAssertNil(StatsReader.projectInfo(projectPath: projectDir.path))
    }

    func testProjectInfoCorruptFileReturnsNil() throws {
        try write("not json at all", to: ".project.json")
        XCTAssertNil(StatsReader.projectInfo(projectPath: projectDir.path))
    }
}
