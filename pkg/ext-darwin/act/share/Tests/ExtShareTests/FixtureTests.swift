import XCTest
@testable import ExtShare

/// Runs `testdata/share/v1` and `testdata/keyboard/v1`, which the Go and Java bindings run as well.
final class FixtureTests: XCTestCase {
    private func expect(_ name: String, _ want: String?, _ codes: Set<String>, _ got: String?) {
        if let want { XCTAssertTrue(codes.contains(want), "\(name): code \(want) is not in codes.json") }
        XCTAssertEqual(got, want, name)
    }

    // MARK: share

    private struct AttachmentSpec: Decodable {
        let name: String
        let mimeType: String?
        let size: Int?
        let text: String?
        let zeros: Int?
        let path: String?

        var data: Data? { text.map { Data($0.utf8) } ?? zeros.map { Data(count: $0) } }
        var fileURL: URL? {
            path.flatMap { $0.hasPrefix("/") ? URL(fileURLWithPath: $0) : URL(string: $0) }
        }

        func build() -> ShareAttachment {
            ShareAttachment(name: name, mimeType: mimeType ?? "", byteCount: size ?? 0, data: data, fileURL: fileURL)
        }
    }

    private struct ItemSpec: Decodable {
        let subject: String?
        let text: String?
        let urls: [String]?
        let attachments: [AttachmentSpec]?

        func build() -> ShareLoadedItem {
            ShareLoadedItem(subject: subject, text: text, urls: urls ?? [], attachments: (attachments ?? []).map { $0.build() })
        }
    }

    private struct PayloadSpec: Decodable {
        let subject: String
        let text: String
        let urls: [String]
        let attachments: [AttachmentSpec]
        let byteCount: Int
    }

    private struct LimitsSpec: Decodable {
        let maxItems: Int?
        let maxBytes: Int?
        let maxTextBytes: Int?
        let maxUrlLength: Int?

        func limits(_ d: LimitsSpec? = nil) throws -> ShareLimits {
            try ShareLimits(maxItems: maxItems ?? d!.maxItems!, maxBytes: maxBytes ?? d!.maxBytes!,
                            maxTextBytes: maxTextBytes ?? d!.maxTextBytes!, maxURLLength: maxUrlLength ?? d!.maxUrlLength!)
        }
    }

    private struct ShareCases: Decodable {
        struct LimitsCase: Decodable { let name: String; let limits: LimitsSpec; let code: String? }
        struct NormalizeCase: Decodable {
            let name: String
            let limits: LimitsSpec?
            let items: [ItemSpec]
            let code: String?
            let payload: PayloadSpec?
        }
        let defaults: LimitsSpec
        let limits: [LimitsCase]
        let normalize: [NormalizeCase]
    }

    func testShareFixtures() throws {
        let cases: ShareCases = try Fixtures.load("share/v1/cases.json")
        let codes = try Fixtures.codes()
        XCTAssertFalse(cases.limits.isEmpty)
        XCTAssertFalse(cases.normalize.isEmpty)
        XCTAssertEqual(ShareLimits.standard, try cases.defaults.limits(cases.defaults), "defaults")
        for c in cases.limits {
            var got: String?
            do { _ = try c.limits.limits(cases.defaults) } catch let error as ShareError { got = error.code } catch { got = "\(error)" }
            expect("limits/\(c.name)", c.code, codes, got)
        }
        for c in cases.normalize {
            let name = "normalize/\(c.name)"
            var got: String?
            var payload: SharePayload?
            do {
                let limits = try (c.limits ?? LimitsSpec(maxItems: nil, maxBytes: nil, maxTextBytes: nil, maxUrlLength: nil)).limits(cases.defaults)
                payload = try ShareNormalizer.normalize(c.items.map { $0.build() }, limits: limits)
            } catch let error as ShareError { got = error.code } catch { got = "\(error)" }
            expect(name, c.code, codes, got)
            guard c.code == nil, let payload, let want = c.payload else { continue }
            XCTAssertEqual(payload.subject ?? "", want.subject, "\(name): subject")
            XCTAssertEqual(payload.text ?? "", want.text, "\(name): text")
            XCTAssertEqual(payload.byteCount, want.byteCount, "\(name): byteCount")
            XCTAssertEqual(payload.urls, want.urls, "\(name): urls")
            XCTAssertEqual(payload.attachments.count, want.attachments.count, "\(name): attachments")
            for (g, w) in zip(payload.attachments, want.attachments) {
                XCTAssertEqual(g.name, w.name, name)
                XCTAssertEqual(g.mimeType, w.mimeType, name)
                XCTAssertEqual(g.byteCount, w.size, name)
                XCTAssertEqual(g.data, w.data, name)
                XCTAssertEqual(g.fileURL?.path, w.path, name)
            }
        }
    }

    // MARK: keyboard

    private final class Proxy: KeyboardTextProxy {
        var keyboardSelectedText: String?
        var keyboardContextBefore: String?
        var keyboardContextAfter: String?
        func insertKeyboardText(_ text: String) {}
        func deleteKeyboardBackward() {}
        func deleteKeyboardForward() {}
    }

    private struct KeyboardCases: Decodable {
        struct Ceilings: Decodable { let maxSelection: Int; let maxInsert: Int }
        struct LimitsCase: Decodable { let name: String; let maxSelection: Int; let maxInsert: Int; let code: String? }
        struct SelectionCase: Decodable { let name: String; let maxSelection: Int; let value: String?; let want: String? }
        struct InsertCase: Decodable { let name: String; let maxInsert: Int; let text: String; let code: String? }
        struct DeleteCase: Decodable { let name: String; let maxInsert: Int; let count: Int; let code: String? }
        struct ContextCase: Decodable { let name: String; let `where`: String; let value: String?; let limit: Int; let want: String? }
        let ceilings: Ceilings
        let limits: [LimitsCase]
        let selection: [SelectionCase]
        let insert: [InsertCase]
        let delete: [DeleteCase]
        let context: [ContextCase]
    }

    private func code(_ body: () throws -> Void) -> String? {
        do { try body(); return nil } catch let error as KeyboardError { return error.code } catch { return "\(error)" }
    }

    func testKeyboardFixtures() throws {
        let cases: KeyboardCases = try Fixtures.load("keyboard/v1/cases.json")
        let codes = try Fixtures.codes()
        XCTAssertEqual(KeyboardLimits.standard, try KeyboardLimits(maxSelectionCharacters: cases.ceilings.maxSelection, maxInsertCharacters: cases.ceilings.maxInsert))
        for c in cases.limits {
            expect("limits/\(c.name)", c.code, codes, code { _ = try KeyboardLimits(maxSelectionCharacters: c.maxSelection, maxInsertCharacters: c.maxInsert) })
        }
        for c in cases.selection {
            let proxy = Proxy()
            proxy.keyboardSelectedText = c.value
            let editor = KeyboardEditor(proxy: proxy, limits: try KeyboardLimits(maxSelectionCharacters: c.maxSelection, maxInsertCharacters: 1))
            XCTAssertEqual(editor.selection(), c.want, "selection/\(c.name)")
        }
        for c in cases.insert {
            let editor = KeyboardEditor(proxy: Proxy(), limits: try KeyboardLimits(maxSelectionCharacters: 1, maxInsertCharacters: c.maxInsert))
            expect("insert/\(c.name)", c.code, codes, code { try editor.insert(c.text) })
        }
        for c in cases.delete {
            let editor = KeyboardEditor(proxy: Proxy(), limits: try KeyboardLimits(maxSelectionCharacters: 1, maxInsertCharacters: c.maxInsert))
            expect("delete/\(c.name) (backward)", c.code, codes, code { try editor.deleteBackward(c.count) })
            expect("delete/\(c.name) (forward)", c.code, codes, code { try editor.deleteForward(c.count) })
        }
        for c in cases.context {
            let proxy = Proxy()
            let editor = KeyboardEditor(proxy: proxy)
            if c.where == "before" {
                proxy.keyboardContextBefore = c.value
                XCTAssertEqual(editor.contextBefore(limit: c.limit), c.want, "context/\(c.name)")
            } else {
                proxy.keyboardContextAfter = c.value
                XCTAssertEqual(editor.contextAfter(limit: c.limit), c.want, "context/\(c.name)")
            }
        }
    }
}
