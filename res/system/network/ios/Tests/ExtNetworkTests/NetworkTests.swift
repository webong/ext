import XCTest
import Darwin
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

#if os(macOS)
    func testNativeDNSValidationAndSearchSemantics() {
        let result = parseNativeDNS([
            "State:/Network/Global/DNS": [
                "ServerAddresses": ["192.0.2.1", "2001:db8::1", "bad::address", "192.0.2.1"],
                "SearchDomains": ["example.test", "example.test"],
                "DomainName": "match-only.test"
            ]
        ])
        XCTAssertEqual(result.servers, ["192.0.2.1", "2001:db8::1"])
        XCTAssertEqual(result.search, ["example.test"])
    }

    func testNativeRouteMaskBoundsAndMalformedMessages() {
        var header = rt_msghdr()
        header.rtm_version = UInt8(RTM_VERSION)
        header.rtm_type = UInt8(RTM_GET)
        header.rtm_addrs = RTA_DST | RTA_GATEWAY | RTA_NETMASK
        header.rtm_msglen = UInt16(MemoryLayout<rt_msghdr>.size + 48)
        var data = withUnsafeBytes(of: &header) { Data($0) }
        for ip in [[192, 0, 2, 7], [192, 0, 2, 1], [255, 255, 255, 0]] {
            var address = [UInt8](repeating: 0, count: 16)
            address[0] = 16; address[1] = UInt8(AF_INET)
            for index in 0..<4 { address[4 + index] = UInt8(ip[index]) }
            data.append(contentsOf: address)
        }
        let (routes, partial) = parseNativeRoutes(data, limit: 1)
        XCTAssertFalse(partial)
        XCTAssertEqual(routes.first?.destination, "192.0.2.0/24")
        XCTAssertEqual(routes.first?.gateway, "192.0.2.1")
        XCTAssertTrue(parseNativeRoutes(data + data, limit: 1).1)
        XCTAssertTrue(parseNativeRoutes(data.dropLast(), limit: 1).1)
        XCTAssertTrue(parseNativeRoutes(Data([0, 0]), limit: 1).1)
    }

    func testNativeMacOSObservation() {
        let snapshot = SystemNetworkObserver().observe()
        XCTAssertTrue(["complete", "partial"].contains(snapshot.routeStatus.state), snapshot.routeStatus.detail ?? "")
        XCTAssertFalse(snapshot.routes.isEmpty)
        XCTAssertTrue(["partial", "unavailable"].contains(snapshot.resolverStatus.state))
    }
#endif
}
