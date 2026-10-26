import XCTest
@testable import ExtNetwork

final class NetworkTests: XCTestCase {
    func testWireNamesAndLocalObservation() throws {
        let snapshot = SystemNetworkObserver().observe()
        XCTAssertFalse(snapshot.platform.isEmpty)
        XCTAssertFalse(snapshot.interfaceStatus.state.isEmpty)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(snapshot)) as? [String: Any])
        XCTAssertNotNil(object["observed_at"])
        XCTAssertNotNil(object["interface_status"])
        XCTAssertNotNil(object["route_status"])
        XCTAssertNotNil(object["resolver_status"])
    }

    func testBoundsAreValidated() {
        XCTAssertThrowsError(try NetworkOptions(maxInterfaces: 0))
        XCTAssertThrowsError(try NetworkOptions(maxAddresses: 65_537))
    }
}
