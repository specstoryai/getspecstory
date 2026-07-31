import XCTest
@testable import SpecStoryKit

final class SessionItemTests: XCTestCase {
    private func indexed(id: String, provider: String = "claude", path: String = "/Users/x/proj", updated: Date? = nil) -> IndexedSession {
        IndexedSession(
            sessionID: id, provider: provider, projectPath: path, title: "local \(id)",
            slug: nil, createdAt: updated?.addingTimeInterval(-600), updatedAt: updated,
            userPromptCount: 3, markdownPath: nil
        )
    }

    private func cloudSession(clientID: String, projectID: String = "proj-1", device: String? = "dev-A", ended: String? = nil) throws -> CloudSession {
        let json = """
        {"id":"srv-\(clientID)","clientId":"\(clientID)","projectId":"\(projectID)","name":"cloud \(clientID)",
         "userTitle":null,"createdAt":"2026-07-30T10:00:00Z","updatedAt":"2026-07-30T11:00:00Z",
         "startedAt":null,"endedAt":\(ended.map { "\"\($0)\"" } ?? "null"),"sessionDataSize":1024,
         "metadata":{"agentName":"codex","deviceId":\(device.map { "\"\($0)\"" } ?? "null"),"machineName":"mbp"}}
        """
        return try JSONDecoder().decode(CloudSession.self, from: Data(json.utf8))
    }

    func testMergeCollapsesSharedClientID() throws {
        let now = Date()
        let merged = SessionItem.merge(
            local: [indexed(id: "s1", updated: now)],
            cloud: [try cloudSession(clientID: "s1"), try cloudSession(clientID: "s2")]
        )
        XCTAssertEqual(merged.count, 2)
        let both = try XCTUnwrap(merged.first { $0.clientID == "s1" })
        XCTAssertEqual(both.origin, .both)
        XCTAssertEqual(both.provider, "claude", "local provider wins on merge")
        XCTAssertEqual(both.projectPath, "/Users/x/proj")
        XCTAssertEqual(both.sessionDataSize, 1024)
        let cloudOnly = try XCTUnwrap(merged.first { $0.clientID == "s2" })
        XCTAssertEqual(cloudOnly.origin, .cloudOnly)
        XCTAssertEqual(cloudOnly.provider, "codex")
    }

    func testMergeSortsByRecency() throws {
        let old = indexed(id: "old", updated: Date(timeIntervalSinceNow: -86400 * 3))
        let fresh = indexed(id: "fresh", updated: Date())
        let merged = SessionItem.merge(local: [old, fresh], cloud: [])
        XCTAssertEqual(merged.map(\.clientID), ["fresh", "old"])
    }

    func testOtherMachineDetection() throws {
        let item = SessionItem(cloud: try cloudSession(clientID: "s9", device: "dev-B"), projectName: nil)
        XCTAssertTrue(item.isFromOtherMachine(currentDeviceID: "dev-A"))
        XCTAssertFalse(item.isFromOtherMachine(currentDeviceID: "dev-B"))
        XCTAssertFalse(item.isFromOtherMachine(currentDeviceID: nil))
    }

    func testFeedGrouping() {
        let calendar = Calendar.current
        let now = Date()
        let today = SessionItem(local: indexed(id: "a", updated: now))
        let yesterday = SessionItem(local: indexed(id: "b", updated: calendar.date(byAdding: .day, value: -1, to: now)))
        let lastMonth = SessionItem(local: indexed(id: "c", updated: calendar.date(byAdding: .day, value: -40, to: now)))
        let sections = FeedSection.group([today, yesterday, lastMonth], now: now, calendar: calendar)
        XCTAssertEqual(sections.count, 3)
        XCTAssertEqual(sections[0].title, "Today")
        XCTAssertEqual(sections[1].title, "Yesterday")
        XCTAssertEqual(sections[0].items.map(\.clientID), ["a"])
    }

    func testProjectNameFromPath() {
        XCTAssertEqual(SessionItem.folderName(of: "/Users/x/getspecstory"), "getspecstory")
        XCTAssertNil(SessionItem.folderName(of: ""))
    }
}
