import XCTest
@testable import ExtSystem

/// Runs `testdata/surface/v1`, which the Go and Java bindings run as well.
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

    private struct Cases: Decodable {
        struct SurfaceOptionsCase: Decodable { let name: String; let maxDisplays: Int; let maxWindows: Int; let code: String? }
        struct AccessibilityOptionsCase: Decodable { let name: String; let maxElements: Int; let maxDepth: Int; let code: String? }
        struct Surface: Decodable { let maxDisplays: Int; let maxWindows: Int }
        struct Defaults: Decodable { let surface: Surface }
        let defaults: Defaults
        let surface: [Case]
        let permission: [Case]
        let accessibility: [Case]
        let surfaceOptions: [SurfaceOptionsCase]
        let accessibilityOptions: [AccessibilityOptionsCase]
    }

    private struct Capability: Decodable {
        struct Item: Decodable { let state: String; let reason: String?; let code: String? }
        let cases: [Item]
    }

    private func code(_ body: () throws -> Void) -> String? {
        do { try body(); return nil }
        catch let error as SurfaceObservationError { return error.code }
        catch let error as AccessibilityObservationError { return error.code }
        catch { return "invalid" } // a snapshot that does not decode
    }

    private func expect(_ name: String, _ want: String?, _ codes: Set<String>, _ got: String?) {
        if let want { XCTAssertTrue(codes.contains(want), "\(name): code \(want) is not in codes.json") }
        XCTAssertEqual(got, want, name)
    }

    func testFixtures() throws {
        let cases: Cases = try Fixtures.load("surface/v1/cases.json")
        let codes = try Fixtures.codes()
        XCTAssertFalse(cases.surface.isEmpty)
        XCTAssertFalse(cases.permission.isEmpty)
        XCTAssertFalse(cases.accessibility.isEmpty)
        for c in cases.surface {
            expect("surface/\(c.name)", c.code, codes, code { try JSONDecoder().decode(SurfaceSnapshot.self, from: c.snapshot).validate() })
        }
        for c in cases.permission {
            expect("permission/\(c.name)", c.code, codes, code { try JSONDecoder().decode(PermissionSnapshot.self, from: c.snapshot).validate() })
        }
        for c in cases.accessibility {
            expect("accessibility/\(c.name)", c.code, codes, code { try JSONDecoder().decode(AccessibilitySnapshot.self, from: c.snapshot).validate() })
        }
        let standard = try SurfaceOptions()
        XCTAssertEqual([standard.maxDisplays, standard.maxWindows], [cases.defaults.surface.maxDisplays, cases.defaults.surface.maxWindows], "defaults")
        for c in cases.surfaceOptions {
            expect("surfaceOptions/\(c.name)", c.code, codes, code { _ = try SurfaceOptions(maxDisplays: c.maxDisplays, maxWindows: c.maxWindows) })
        }
        for c in cases.accessibilityOptions {
            expect("accessibilityOptions/\(c.name)", c.code, codes, code { _ = try AccessibilityOptions(maxElements: c.maxElements, maxDepth: c.maxDepth) })
        }
        let capability: Capability = try Fixtures.load("capability/v1/cases.json")
        for item in capability.cases {
            expect("capability/\(item.state)\(item.reason.map { "+\($0)" } ?? "")", item.code, codes,
                   code { try SurfaceCollectionStatus(state: item.state, reason: item.reason).validate() })
        }
    }

    @MainActor
    func testTheObserversProduceValidSnapshots() throws {
        try SystemSurfaceObserver().observe().validate()
        try SystemPermissionObserver.observe().validate()
        try SystemAccessibilityObserver().observe().validate()
    }
}
