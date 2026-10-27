import Foundation
import XCTest
@testable import ExtSystem

final class ExtensionTests: XCTestCase {
    func testShareLimitsRespectCeilings() {
        XCTAssertNoThrow(try ShareLimits())
        XCTAssertThrowsError(try ShareLimits(maxItems: ShareLimits.maxItems + 1))
        XCTAssertThrowsError(try ShareLimits(maxBytes: ShareLimits.maxBytes + 1))
        XCTAssertThrowsError(try ShareLimits(maxTextBytes: 0))
        XCTAssertThrowsError(try ShareLimits(maxURLLength: ShareLimits.maxURLLength + 1))
        XCTAssertEqual(ShareLimits.standard.maxItems, 32)
    }

    func testNormalizeMergesAndDeduplicates() throws {
        let attachment = ShareAttachment(
            name: "report.txt", mimeType: "text/plain", byteCount: 0, data: Data("hello".utf8)
        )
        let payload = try ShareNormalizer.normalize([
            ShareLoadedItem(subject: "Notes", text: "first",
                            urls: ["https://example.test/a"]),
            ShareLoadedItem(subject: "Notes", text: "first",
                            urls: ["https://example.test/a", " HTTP://example.test/b "]),
            ShareLoadedItem(text: "second", attachments: [attachment, attachment]),
        ])
        XCTAssertEqual(payload.subject, "Notes")
        XCTAssertEqual(payload.text, "first\nsecond")
        XCTAssertEqual(payload.urls, ["https://example.test/a", "http://example.test/b"])
        XCTAssertEqual(payload.attachments.count, 1)
        XCTAssertEqual(payload.attachments.first?.byteCount, 5)
        XCTAssertEqual(payload.byteCount, 5)
    }

    func testNormalizeRejectsConflictingAttachmentNames() {
        XCTAssertThrowsError(try ShareNormalizer.normalize([
            ShareLoadedItem(attachments: [
                ShareAttachment(name: "photo.jpg", mimeType: "image/jpeg", byteCount: 0, data: Data("a".utf8)),
                ShareAttachment(name: "Photo.jpg", mimeType: "image/jpeg", byteCount: 0, data: Data("b".utf8)),
            ]),
        ]))
    }

    func testNormalizeEnforcesBounds() {
        let oversized = ShareAttachment(name: "large.bin", mimeType: "application/octet-stream",
                                        byteCount: 0, data: Data(repeating: 1, count: 64))
        XCTAssertThrowsError(try ShareNormalizer.normalize(
            [ShareLoadedItem(attachments: [oversized])],
            limits: try ShareLimits(maxBytes: 32)
        ))
        XCTAssertThrowsError(try ShareNormalizer.normalize(
            [ShareLoadedItem(text: "0123456789"), ShareLoadedItem(text: "abcdefghij")],
            limits: try ShareLimits(maxTextBytes: 12)
        ))
        XCTAssertThrowsError(try ShareNormalizer.normalize(
            Array(repeating: ShareLoadedItem(text: "value"), count: 3),
            limits: try ShareLimits(maxItems: 2)
        ))
    }

    func testNormalizeRejectsUnsafeNamesAndLinks() {
        for name in ["../escape", "nested/file", "", "..", "nul\u{0}"] {
            XCTAssertThrowsError(try ShareNormalizer.normalize([
                ShareLoadedItem(attachments: [
                    ShareAttachment(name: name, mimeType: "text/plain", byteCount: 1),
                ]),
            ]), name)
        }
        XCTAssertThrowsError(try ShareNormalizer.normalize([
            ShareLoadedItem(urls: ["not a url"]),
        ]))
        XCTAssertThrowsError(try ShareNormalizer.normalize([
            ShareLoadedItem(urls: ["https://example.test/\u{0}"]),
        ]))
    }

    func testAttachmentRequiresExactlyOneContentSource() {
        XCTAssertThrowsError(try ShareNormalizer.normalize([
            ShareLoadedItem(attachments: [
                ShareAttachment(name: "value", mimeType: "text/plain", byteCount: 1,
                                data: Data([1]), fileURL: URL(fileURLWithPath: "/tmp/value")),
            ]),
        ]))
        XCTAssertThrowsError(try ShareNormalizer.normalize([
            ShareLoadedItem(attachments: [
                ShareAttachment(name: "value", mimeType: "text/plain", byteCount: 1,
                                fileURL: URL(string: "https://example.test/value")!),
            ]),
        ]))
    }

    func testAppGroupSinkRequiresExplicitPortableIdentity() {
        XCTAssertThrowsError(try AppGroupAttachmentSink(groupIdentifier: ""))
        XCTAssertThrowsError(try AppGroupAttachmentSink(groupIdentifier: "group.test", directoryName: "../escape"))
        XCTAssertNoThrow(try AppGroupAttachmentSink(groupIdentifier: "group.test", directoryName: "Inbox"))
    }

    func testKeyboardSelectionIsBoundedAndTrimmed() {
        let proxy = FakeKeyboardProxy()
        let editor = KeyboardEditor(proxy: proxy)

        proxy.selection = "  value \n"
        XCTAssertEqual(editor.selection(), "value")

        proxy.selection = " \n\t "
        XCTAssertNil(editor.selection())

        proxy.selection = String(repeating: "x", count: 32)
        let narrow = KeyboardEditor(proxy: proxy, limits: try! KeyboardLimits(maxSelectionCharacters: 8))
        XCTAssertNil(narrow.selection())
    }

    func testKeyboardEditingBounds() throws {
        let proxy = FakeKeyboardProxy()
        let editor = KeyboardEditor(
            proxy: proxy, limits: try KeyboardLimits(maxInsertCharacters: 8)
        )
        XCTAssertEqual(try editor.insert("hello"), true)
        XCTAssertEqual(proxy.insertions, ["hello"])
        XCTAssertThrowsError(try editor.insert(""))
        XCTAssertThrowsError(try editor.insert("nul\u{0}"))
        XCTAssertThrowsError(try editor.insert("too long for bounds"))
        XCTAssertThrowsError(try editor.deleteBackward(-1))
        try editor.deleteBackward(2)
        XCTAssertEqual(proxy.backwardDeletes, 2)
        try editor.deleteForward(1)
        XCTAssertEqual(proxy.forwardDeletes, 1)
        XCTAssertEqual(try editor.submit(), true)
        XCTAssertEqual(proxy.insertions.last, "\n")
        XCTAssertFalse(editor.canSwitchKeyboard)
        XCTAssertFalse(editor.nextKeyboard())
    }

    func testKeyboardContextIsWindowed() {
        let proxy = FakeKeyboardProxy()
        proxy.before = "0123456789"
        proxy.after = "abcdefghij"
        let editor = KeyboardEditor(proxy: proxy)
        XCTAssertEqual(editor.contextBefore(limit: 4), "6789")
        XCTAssertEqual(editor.contextAfter(limit: 4), "abcd")
        XCTAssertNil(editor.contextBefore(limit: 0))
    }

    func testKeyboardSwitchesOnlyThroughHost() {
        let proxy = FakeKeyboardProxy()
        let host = FakeKeyboardHost()
        let editor = KeyboardEditor(proxy: proxy, host: host)
        XCTAssertTrue(editor.canSwitchKeyboard)
        XCTAssertTrue(editor.nextKeyboard())
        XCTAssertEqual(host.advances, 1)
    }
}

private final class FakeKeyboardProxy: KeyboardTextProxy {
    var selection: String?
    var before: String?
    var after: String?
    var insertions: [String] = []
    var backwardDeletes = 0
    var forwardDeletes = 0

    var keyboardSelectedText: String? { selection }
    var keyboardContextBefore: String? { before }
    var keyboardContextAfter: String? { after }
    func insertKeyboardText(_ text: String) { insertions.append(text) }
    func deleteKeyboardBackward() { backwardDeletes += 1 }
    func deleteKeyboardForward() { forwardDeletes += 1 }
}

private final class FakeKeyboardHost: KeyboardHost {
    var advances = 0
    func advanceToNextInputMode() { advances += 1 }
}
