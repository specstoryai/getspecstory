import XCTest
@testable import SpecStoryKit

final class DeviceIdentityTests: XCTestCase {
    func testDeviceIDIsStable64CharHex() {
        let id = DeviceIdentity.current
        XCTAssertEqual(id.count, 64)
        XCTAssertTrue(id.allSatisfy { $0.isHexDigit })
        XCTAssertEqual(id, DeviceIdentity.computeDeviceID(), "derivation must be deterministic")
    }

    func testMachineNameNonEmpty() {
        XCTAssertFalse(DeviceIdentity.machineName.isEmpty)
    }
}
