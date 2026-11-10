import XCTest
@testable import ExtClipboard

/// Runs `testdata/clipboard/v1`, which the Go, Java and C# bindings run as well.
final class FixtureTests: XCTestCase {
    private struct Representation: Decodable {
        let format: String
        let text: String?
        let zeros: Int?
    }

    private struct Item: Decodable {
        let representations: [Representation]
        let times: Int?

        func build() -> [ClipboardItem] {
            let item = ClipboardItem(representations: representations.map {
                ClipboardRepresentation(format: $0.format, data: $0.text.map { Data($0.utf8) } ?? Data(count: $0.zeros ?? 0))
            })
            return Array(repeating: item, count: times ?? 1)
        }
    }

    private struct Cases: Decodable {
        struct PolicyCase: Decodable {
            let name: String
            let maxBytes: Int
            let formats: [String]
            let code: String?
        }
        struct ItemsCase: Decodable {
            let name: String
            let maxBytes: Int
            let formats: [String]
            let items: [Item]
            let code: String?
        }
        let policy: [PolicyCase]
        let items: [ItemsCase]
    }

    private func check(_ name: String, _ want: String?, _ codes: Set<String>, _ body: () throws -> Void) {
        var got: String?
        do { try body() } catch let error as ClipboardError { got = error.code } catch { got = "\(error)" }
        if let want { XCTAssertTrue(codes.contains(want), "\(name): code \(want) is not in codes.json") }
        XCTAssertEqual(got, want, name)
    }

    func testFixtures() throws {
        let cases: Cases = try Fixtures.load("clipboard/v1/cases.json")
        let codes = try Fixtures.codes()
        XCTAssertFalse(cases.policy.isEmpty)
        XCTAssertFalse(cases.items.isEmpty)
        for c in cases.policy {
            check("policy/\(c.name)", c.code, codes) {
                try ClipboardPolicy(maxBytes: c.maxBytes, allowedFormats: c.formats).validate()
            }
        }
        for c in cases.items {
            check("items/\(c.name)", c.code, codes) {
                try ClipboardValidation.validate(items: c.items.flatMap { $0.build() }, maxBytes: c.maxBytes, formats: c.formats)
            }
        }
    }
}
