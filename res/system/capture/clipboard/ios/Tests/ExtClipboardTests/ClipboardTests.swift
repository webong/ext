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

    /// A clipboard over the general pasteboard on macOS and a private one on iOS. The bound tests
    /// stop before any native call, so the general pasteboard is neither read nor changed.
    @MainActor
    private func clipboard(timeout: TimeInterval) throws -> SystemClipboard {
        let policy = ClipboardPolicy(maxBytes: 1024, allowedFormats: ["public.utf8-plain-text"], allowRead: true, allowWrite: true)
#if os(iOS)
        let board = try XCTUnwrap(UIPasteboard(name: UIPasteboard.Name(rawValue: "io.github.webong.ext.clipboard.bound"), create: true))
        return try SystemClipboard(policy: policy, board: board, timeout: timeout)
#else
        return try SystemClipboard(policy: policy, timeout: timeout)
#endif
    }

    @MainActor
    func testCallsAreBoundedInTime() throws {
        for timeout in [0, -1, 3_601, .infinity, .nan] as [TimeInterval] {
            XCTAssertThrowsError(try clipboard(timeout: timeout), "timeout \(timeout)") { error in
                XCTAssertEqual((error as? ClipboardError)?.code, "invalid")
            }
        }
        XCTAssertEqual(try clipboard(timeout: 5).timeout, 5)
        let expired = try clipboard(timeout: 1e-9)
        XCTAssertThrowsError(try expired.read()) { error in
            XCTAssertEqual(error as? ClipboardError, .timeout)
            XCTAssertEqual((error as? ClipboardError)?.code, "timeout")
        }
        XCTAssertThrowsError(try expired.write(expectedGeneration: "p:1", items: [])) { error in
            XCTAssertEqual(error as? ClipboardError, .timeout)
        }
    }

    func testCallsAreCanceledWithTheirTask() async throws {
        let task = Task { @MainActor () -> [Error?] in
            while !Task.isCancelled { await Task.yield() }
            let clipboard = try self.clipboard(timeout: 5)
            var errors: [Error?] = []
            do { _ = try clipboard.read(); errors.append(nil) } catch { errors.append(error) }
            do { _ = try clipboard.clear(expectedGeneration: "p:1"); errors.append(nil) } catch { errors.append(error) }
            return errors
        }
        task.cancel()
        let errors = try await task.value
        XCTAssertEqual(errors.count, 2)
        for error in errors {
            XCTAssertEqual(error as? ClipboardError, .canceled)
            XCTAssertEqual((error as? ClipboardError)?.code, "canceled")
        }
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
