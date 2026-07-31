import XCTest
@testable import SpecStoryKit

final class WatchEventTests: XCTestCase {
    func testParsesCreatedEvent() throws {
        let line = """
        {"timestamp":"2026-07-31T15:04:05Z","action":"created","session_id":"abc-123","start_time":"2026-07-31T15:00:00Z","end_time":"2026-07-31T15:04:00Z","provider":"claude","markdown_size":2048,"total_user_prompts":3,"agent_activity":7,"markdown_file":"/tmp/p/.specstory/history/x.md"}
        """
        let event = try XCTUnwrap(WatchEvent.parse(line: line))
        XCTAssertEqual(event.action, .created)
        XCTAssertEqual(event.sessionID, "abc-123")
        XCTAssertEqual(event.provider, "claude")
        XCTAssertEqual(event.markdownSize, 2048)
        XCTAssertEqual(event.totalUserPrompts, 3)
        XCTAssertEqual(event.agentActivity, 7)
        XCTAssertEqual(event.markdownFile, "/tmp/p/.specstory/history/x.md")
    }

    func testParsesEventWithoutMarkdownFile() throws {
        let line = """
        {"timestamp":"2026-07-31T15:04:05Z","action":"updated","session_id":"abc","start_time":null,"end_time":null,"provider":"codex","markdown_size":10,"total_user_prompts":1,"agent_activity":0}
        """
        let event = try XCTUnwrap(WatchEvent.parse(line: line))
        XCTAssertEqual(event.action, .updated)
        XCTAssertNil(event.markdownFile)
    }

    func testRejectsSlogLines() {
        XCTAssertNil(WatchEvent.parse(line: "time=2026-07-31T15:04:05Z level=INFO msg=\"watching\""))
        XCTAssertNil(WatchEvent.parse(line: ""))
        XCTAssertNil(WatchEvent.parse(line: "{not json"))
    }

    func testProviderRegistryRoundTrip() {
        for provider in Provider.allCases {
            XCTAssertEqual(Provider(providerID: provider.rawValue.uppercased()), provider)
        }
        XCTAssertNil(Provider(providerID: "not-an-agent"))
    }
}
