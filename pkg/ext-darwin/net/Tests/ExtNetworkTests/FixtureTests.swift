import XCTest
@testable import ExtNetwork

/// Runs `testdata/network/v1` and `testdata/capability/v1`, which the Go and Java bindings run as well.
final class FixtureTests: XCTestCase {
    private struct Cases: Decodable {
        struct Defaults: Decodable { let maxInterfaces: Int; let maxAddresses: Int; let maxRoutes: Int }
        struct OptionsCase: Decodable { let name: String; let maxInterfaces: Int; let maxAddresses: Int; let maxRoutes: Int; let code: String? }
        struct Want: Decodable { let ip: String; let prefixLength: Int; let family: String; let scope: String }
        struct AddressCase: Decodable { let name: String; let ip: String; let prefix: Int; let code: String?; let want: Want? }
        struct Lists: Decodable { let servers: [String]; let search: [String]; let options: [String] }
        struct ResolverCase: Decodable { let name: String; let servers: [String]; let search: [String]; let options: [String]; let want: Lists }
        let defaults: Defaults
        let options: [OptionsCase]
        let addresses: [AddressCase]
        let resolver: [ResolverCase]
    }

    private struct Capability: Decodable {
        struct Case: Decodable { let state: String; let reason: String?; let code: String? }
        let cases: [Case]
    }

    private func code(_ body: () throws -> Void) -> String? {
        do { try body(); return nil } catch let error as NetworkObservationError { return error.code } catch { return "\(error)" }
    }

    private func expect(_ name: String, _ want: String?, _ codes: Set<String>, _ got: String?) {
        if let want { XCTAssertTrue(codes.contains(want), "\(name): code \(want) is not in codes.json") }
        XCTAssertEqual(got, want, name)
    }

    func testFixtures() throws {
        let cases: Cases = try Fixtures.load("network/v1/cases.json")
        let codes = try Fixtures.codes()
        XCTAssertFalse(cases.options.isEmpty)
        XCTAssertFalse(cases.addresses.isEmpty)
        XCTAssertFalse(cases.resolver.isEmpty)
        let standard = try NetworkOptions()
        XCTAssertEqual([standard.maxInterfaces, standard.maxAddresses, standard.maxRoutes],
                       [cases.defaults.maxInterfaces, cases.defaults.maxAddresses, cases.defaults.maxRoutes], "defaults")
        for c in cases.options {
            expect("options/\(c.name)", c.code, codes, code { _ = try NetworkOptions(maxInterfaces: c.maxInterfaces, maxAddresses: c.maxAddresses, maxRoutes: c.maxRoutes) })
        }
        for c in cases.addresses {
            var got: NetworkAddress?
            let result = code { got = try NetworkAddress.parse(c.ip, prefixLength: c.prefix) }
            expect("addresses/\(c.name)", c.code, codes, result)
            if let want = c.want, let got {
                XCTAssertEqual(got, NetworkAddress(ip: want.ip, prefixLength: want.prefixLength, family: want.family, scope: want.scope), "addresses/\(c.name)")
            }
        }
        for c in cases.resolver {
            XCTAssertEqual(NetworkResolver.clean(c.servers), c.want.servers, "resolver/\(c.name): servers")
            XCTAssertEqual(NetworkResolver.clean(c.search), c.want.search, "resolver/\(c.name): search")
            XCTAssertEqual(NetworkResolver.clean(c.options), c.want.options, "resolver/\(c.name): options")
        }
        let capability: Capability = try Fixtures.load("capability/v1/cases.json")
        XCTAssertFalse(capability.cases.isEmpty)
        for c in capability.cases {
            expect("capability/\(c.state)\(c.reason.map { "+\($0)" } ?? "")", c.code, codes, code {
                try NetworkCollectionStatus(state: c.state, reason: c.reason).validate()
            })
        }
    }
}
