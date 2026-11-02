import XCTest
@testable import ExtClipboard
#if os(iOS)
import UIKit
#endif

final class ClipboardTests: XCTestCase {
    func testPolicyAndBounds() throws {
        try ClipboardPolicy(maxBytes: 8, allowedFormats: ["text/plain"]).validate()
        XCTAssertThrowsError(try ClipboardPolicy(maxBytes: 8, allowedFormats: ["text/plain", "text/plain"]).validate())
        let item = ClipboardItem(representations: [ClipboardRepresentation(format: "text/plain", data: Data("hello".utf8))])
        try ClipboardValidation.validate(items: [item], maxBytes: 5, formats: ["text/plain"])
        XCTAssertThrowsError(try ClipboardValidation.validate(items: [item], maxBytes: 4, formats: ["text/plain"]))
        XCTAssertThrowsError(try ClipboardValidation.validate(items: [item], maxBytes: 5, formats: ["text/html"]))
    }

    func testWireNamesAndData() throws {
        let value = ClipboardSnapshot(generation: "p:1", items: [ClipboardItem(representations: [
            ClipboardRepresentation(format: "text/plain", data: Data([0, 1, 2]))
        ])])
        let encoded = try JSONEncoder().encode(value)
        XCTAssertEqual(try JSONDecoder().decode(ClipboardSnapshot.self, from: encoded), value)
        XCTAssertTrue(String(decoding: encoded, as: UTF8.self).contains("\"generation\""))
    }

#if os(iOS)
    /// Runs over a private named pasteboard. Reading the general pasteboard blocks in a test
    /// runner that has no host application (the Simulator's xctest tool), which is how these
    /// tests run, so the general pasteboard binding is not exercised here.
    @MainActor
    func testNativeClipboardRoundTrip() throws {
        let format = "public.utf8-plain-text"
        let name = UIPasteboard.Name(rawValue: "io.github.webong.ext.clipboard.tests")
        let board = try XCTUnwrap(UIPasteboard(name: name, create: true))
        defer { UIPasteboard.remove(withName: name) }
        let clipboard = try SystemClipboard(policy: ClipboardPolicy(
            maxBytes: 1024,
            allowedFormats: [format],
            allowRead: true,
            allowWrite: true
        ), board: board)
        let initial = try clipboard.currentGeneration()
        let item = ClipboardItem(representations: [
            ClipboardRepresentation(format: format, data: Data("ext clipboard".utf8))
        ])
        let receipt = try clipboard.write(expectedGeneration: initial, items: [item])
        let snapshot = try clipboard.read()
        XCTAssertEqual(snapshot.generation, receipt.generation)
        XCTAssertEqual(snapshot.items, [item])
        XCTAssertThrowsError(try clipboard.write(expectedGeneration: initial, items: [item])) { error in
            XCTAssertEqual(error as? ClipboardError, .changed)
        }
        _ = try clipboard.clear(expectedGeneration: snapshot.generation)
        XCTAssertTrue(try clipboard.read().items.isEmpty)
    }
#endif
}
