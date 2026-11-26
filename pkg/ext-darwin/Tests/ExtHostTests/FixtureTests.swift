import XCTest
@testable import ExtHost

/// Runs `testdata/host/v1` and `testdata/capability/v1`, which the Go and Java bindings run as well.
final class FixtureTests: XCTestCase {
    private struct Case: Decodable {
        let name: String
        let snapshot: Data
        let code: String?

        enum CodingKeys: String, CodingKey { case name, snapshot, code }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            name = try c.decode(String.self, forKey: .name)
            code = try c.decodeIfPresent(String.self, forKey: .code)
            // Keep the snapshot as the JSON text it is: it is decoded as a consumer would decode it.
            let raw = try c.decode(AnyJSON.self, forKey: .snapshot)
            snapshot = try JSONSerialization.data(withJSONObject: raw.value, options: [.fragmentsAllowed])
        }
    }

    private struct AnyJSON: Decodable {
        let value: Any
        init(from decoder: Decoder) throws {
            let c = try decoder.singleValueContainer()
            if c.decodeNil() { value = NSNull() }
            else if let v = try? c.decode(Bool.self) { value = v }
            else if let v = try? c.decode(Int64.self) { value = v }
            else if let v = try? c.decode(Double.self) { value = v }
            else if let v = try? c.decode(String.self) { value = v }
            else if let v = try? c.decode([AnyJSON].self) { value = v.map { $0.value } }
            else { value = try c.decode([String: AnyJSON].self).mapValues { $0.value } }
        }
    }

    private struct Cases: Decodable { let cases: [Case] }

    private struct Capability: Decodable {
        struct Item: Decodable { let state: String; let reason: String?; let code: String? }
        let cases: [Item]
    }

    private func code(_ body: () throws -> Void) -> String? {
        do { try body(); return nil } catch let error as ResourceObservationError { return error.code } catch { return "invalid" }
    }

    func testFixtures() throws {
        let cases: Cases = try Fixtures.load("host/v1/cases.json")
        let codes = try Fixtures.codes()
        XCTAssertFalse(cases.cases.isEmpty)
        for c in cases.cases {
            let got = code { try JSONDecoder().decode(ResourceInfo.self, from: c.snapshot).validate() }
            if let want = c.code { XCTAssertTrue(codes.contains(want), "\(c.name): code \(want) is not in codes.json") }
            XCTAssertEqual(got, c.code, "snapshots/\(c.name)")
        }
        let capability: Capability = try Fixtures.load("capability/v1/cases.json")
        for item in capability.cases {
            XCTAssertEqual(code { try ResourceCollectionStatus(state: item.state, reason: item.reason).validate() }, item.code,
                           "capability/\(item.state)\(item.reason.map { "+\($0)" } ?? "")")
        }
    }

    @MainActor
    func testTheObserverProducesValidSnapshots() throws {
        let value = try SystemResourceObserver().observe()
        try value.validate()
        let again = try JSONDecoder().decode(ResourceInfo.self, from: JSONEncoder().encode(value))
        XCTAssertEqual(again, value)
    }
}
