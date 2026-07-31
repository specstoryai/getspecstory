import XCTest
@testable import SpecStoryKit

final class CloudModelsTests: XCTestCase {
    // MARK: - CloudSession

    func testCloudSessionTolerantDecodingIgnoresUnknownFields() throws {
        let json = """
        {"id":"srv-uuid","clientId":"client-1","projectId":"proj-1","name":"session name",
         "createdAt":"2026-07-30T10:00:00.500Z","updatedAt":"2026-07-30T11:00:00Z",
         "startedAt":"2026-07-30T10:00:01Z",
         "workspaceId":"ws-uuid","markdownSize":123,"shareStatus":"private",
         "exchanges":[{"whatever":true}],
         "metadata":{"agentName":"Codex","clientName":"specstory-cli","summary":"ignored extra"}}
        """
        let session = try JSONDecoder().decode(CloudSession.self, from: Data(json.utf8))
        XCTAssertEqual(session.id, "srv-uuid")
        XCTAssertEqual(session.clientId, "client-1")
        XCTAssertEqual(session.name, "session name")
        XCTAssertNil(session.userTitle)
        XCTAssertNil(session.endedAt)
        XCTAssertNil(session.sessionDataSize)
        XCTAssertEqual(session.metadata.agentName, "Codex")
        XCTAssertNil(session.metadata.deviceId)
    }

    func testCloudSessionDateAccessorsParseBothISOForms() throws {
        let session = CloudSession(id: "a", clientId: "b", projectId: "c", name: "d",
                                   createdAt: "2026-07-30T10:00:00.500Z",
                                   updatedAt: "2026-07-30T11:00:00Z",
                                   startedAt: nil)
        let created = try XCTUnwrap(session.createdAtDate, "fractional seconds form parses")
        let updated = try XCTUnwrap(session.updatedAtDate, "plain form parses")
        XCTAssertEqual(updated.timeIntervalSince(created), 3599.5, accuracy: 0.001)
        XCTAssertNil(session.startedAtDate)
    }

    func testCloudSessionMissingMetadataDefaultsToEmpty() throws {
        let json = #"{"id":"x","clientId":"y","projectId":"z","name":"n","createdAt":"","updatedAt":""}"#
        let session = try JSONDecoder().decode(CloudSession.self, from: Data(json.utf8))
        XCTAssertEqual(session.metadata, CloudSessionMetadata())
    }

    func testDisplayTitlePrecedence() {
        let both = CloudSession(id: "1", clientId: "c", projectId: "p", name: "the name",
                                userTitle: "User Title",
                                metadata: CloudSessionMetadata(title: "Meta Title"))
        XCTAssertEqual(both.displayTitle, "User Title")

        let metaOnly = CloudSession(id: "1", clientId: "c", projectId: "p", name: "the name",
                                    metadata: CloudSessionMetadata(title: "Meta Title"))
        XCTAssertEqual(metaOnly.displayTitle, "Meta Title")

        let nameOnly = CloudSession(id: "1", clientId: "c", projectId: "p", name: "the name")
        XCTAssertEqual(nameOnly.displayTitle, "the name")
    }

    // MARK: - DeviceMetadata

    func testDeviceMetadataCurrentUsesFixedIdentity() {
        let metadata = DeviceMetadata.current(clientVersion: "2.3.4")
        XCTAssertEqual(metadata.os, "darwin")
        XCTAssertEqual(metadata.osDisplayName, "macOS")
        XCTAssertEqual(metadata.client, "specstory-macapp")
        XCTAssertEqual(metadata.clientVersion, "2.3.4")
        XCTAssertFalse(metadata.hostname.isEmpty)
        XCTAssertFalse(metadata.username.isEmpty)
        XCTAssertTrue(["arm64", "x86_64"].contains(metadata.architecture))
        // os_version looks like 14.5.0
        XCTAssertEqual(metadata.osVersion.split(separator: ".").count, 3)
    }

    func testDeviceMetadataEncodesSnakeCaseKeys() throws {
        let metadata = DeviceMetadata(hostname: "h", os: "darwin", osVersion: "14.0.0",
                                      osDisplayName: "macOS", architecture: "arm64", username: "u",
                                      client: "specstory-macapp", clientVersion: "1.0.0")
        let data = try JSONEncoder().encode(metadata)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["os_version"] as? String, "14.0.0")
        XCTAssertEqual(object["os_display_name"] as? String, "macOS")
        XCTAssertEqual(object["client_version"] as? String, "1.0.0")
        XCTAssertNil(object["osVersion"])
    }

    // MARK: - Auth results

    func testDeviceLoginResultDecodesNestedUserEmail() throws {
        let json = """
        {"refreshToken":"r","createdAt":"2026-07-31T00:00:00Z","expiresAt":"2036-07-31T00:00:00Z",
         "user":{"email":"a@b.com","id":"ignored"}}
        """
        let result = try JSONDecoder().decode(DeviceLoginResult.self, from: Data(json.utf8))
        XCTAssertEqual(result.refreshToken, "r")
        XCTAssertEqual(result.email, "a@b.com")
        XCTAssertNotNil(result.expiresAtDate)
    }

    func testAccessTokenResultParsesExpiry() throws {
        let json = #"{"accessToken":"t","createdAt":"2026-07-31T00:00:00Z","expiresAt":"2026-07-31T01:00:00.250Z"}"#
        let result = try JSONDecoder().decode(AccessTokenResult.self, from: Data(json.utf8))
        XCTAssertEqual(result.accessToken, "t")
        XCTAssertNotNil(result.expiresAtDate)
    }

    // MARK: - Entitlement

    func testEntitlementDecodesFlatShape() throws {
        let json = #"{"plan":"pro","features":{"resume":true,"skills":false}}"#
        let entitlement = try JSONDecoder().decode(Entitlement.self, from: Data(json.utf8))
        XCTAssertEqual(entitlement.plan, "pro")
        XCTAssertTrue(entitlement.isEnabled("resume"))
        XCTAssertFalse(entitlement.isEnabled("skills"))
    }

    func testEntitlementDecodesNestedShapeAndTossesNonBoolFeatures() throws {
        let json = #"{"entitlement":{"plan":"free","features":{"resume":false,"maxSessions":100}}}"#
        let entitlement = try JSONDecoder().decode(Entitlement.self, from: Data(json.utf8))
        XCTAssertEqual(entitlement.plan, "free")
        XCTAssertFalse(entitlement.isEnabled("resume"))
        XCTAssertFalse(entitlement.isEnabled("maxSessions"), "non-boolean values are skipped, fail closed")
    }

    func testEntitlementFailsClosedOnEmptyBody() throws {
        let entitlement = try JSONDecoder().decode(Entitlement.self, from: Data("{}".utf8))
        XCTAssertNil(entitlement.plan)
        XCTAssertFalse(entitlement.isEnabled("anything"))
    }

    // MARK: - UserTool + SearchHit

    func testUserToolTolerantDecoding() throws {
        let json = #"{"agentName":"Claude Code","sessionCount":12,"lastUsed":"2026-07-30T10:00:00Z","extra":1}"#
        let tool = try JSONDecoder().decode(UserTool.self, from: Data(json.utf8))
        XCTAssertEqual(tool.agentName, "Claude Code")
        XCTAssertEqual(tool.sessionCount, 12)
        XCTAssertNotNil(tool.lastUsedDate)
    }

    func testSearchHitDecodesIntegerRank() throws {
        let json = #"{"id":"s1","rank":3}"#
        let hit = try JSONDecoder().decode(SearchHit.self, from: Data(json.utf8))
        XCTAssertEqual(hit.rank, 3.0)
        XCTAssertNil(hit.project)
    }
}
