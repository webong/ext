import Foundation
import XCTest
@testable import ExtWeb

final class UserscriptStoreTests: XCTestCase {
    private func temporary() throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }; return root
    }
    func testGoRecordLifecycleAndIsolation() throws {
        let root = try temporary()
        let r = try Fixtures.load("userscript/store/record.json", as: UserscriptRecord.self)
        let store = UserscriptStore(directory: root)
        XCTAssertEqual(try store.list(target: r.target), [])
        XCTAssertThrowsError(try store.save(r, replace: true))
        try store.save(r)
        XCTAssertEqual(try UserscriptStore(directory: root).load(target: r.target, id: r.id), r)
        XCTAssertThrowsError(try store.save(r))
        XCTAssertEqual(try store.list(target: "other"), [])
        XCTAssertThrowsError(try store.load(target: "other", id: r.id))
        let disabled = try store.setEnabled(target: r.target, id: r.id, enabled: false)
        XCTAssertEqual(disabled.revision, r.revision)
        XCTAssertFalse(try store.load(target: r.target, id: r.id).enabled)
        var bad = r; bad.source += "tampered"
        XCTAssertThrowsError(try store.save(bad, replace: true))
        XCTAssertEqual(try store.load(target: r.target, id: r.id), disabled)
        var another = r; another.id = "aaa"; another.revision = Userscript.revision(of: another)
        try store.save(another)
        XCTAssertEqual(try store.list(target: r.target).map(\.id), ["aaa", r.id])
        try store.remove(target: r.target, id: r.id)
        XCTAssertThrowsError(try store.load(target: r.target, id: r.id))
        XCTAssertThrowsError(try store.remove(target: r.target, id: r.id))
        XCTAssertThrowsError(try store.load(target: r.target, id: "../outside"))
        XCTAssertThrowsError(try store.list(target: " "))
    }
    func testCorruptAndSymlinkRecordsAreRejected() throws {
        let root = try temporary()
        let r = try Fixtures.load("userscript/store/record.json", as: UserscriptRecord.self)
        let store = UserscriptStore(directory: root); try store.save(r)
        let folder = try XCTUnwrap(FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil).first)
        XCTAssertEqual(folder.lastPathComponent, "edcb219f80355327e74351977daf7a16144d20773871f784ab3d5c725acc7ad0")
        let file = folder.appendingPathComponent(r.id + ".json")
        let permission = try FileManager.default.attributesOfItem(atPath: file.path)[.posixPermissions] as? Int
        XCTAssertEqual(permission, 0o600)
        let oversized = try FileHandle(forWritingTo: file)
        oversized.truncateFile(atOffset: UInt64(UserscriptStore.maxRecordBytes + 1)); oversized.closeFile()
        XCTAssertThrowsError(try store.load(target: r.target, id: r.id))
        try Data("{}".utf8).write(to: file)
        XCTAssertThrowsError(try store.load(target: r.target, id: r.id))
        XCTAssertThrowsError(try store.list(target: r.target))
        try FileManager.default.removeItem(at: file)
        let outside = root.appendingPathComponent("outside")
        try JSONEncoder().encode(r).write(to: outside)
        try FileManager.default.createSymbolicLink(at: file, withDestinationURL: outside)
        XCTAssertThrowsError(try store.load(target: r.target, id: r.id))
        XCTAssertThrowsError(try store.save(r, replace: true))
        XCTAssertThrowsError(try store.remove(target: r.target, id: r.id))
        try FileManager.default.removeItem(at: file)
        try FileManager.default.createSymbolicLink(at: file, withDestinationURL: root.appendingPathComponent("missing"))
        XCTAssertThrowsError(try store.save(r))
    }
    func testManagementLifecycle() throws {
        let store = UserscriptStore(directory: try temporary())
        let input = #"{"id":"script","name":"Script","matches":["https://example.com/*"],"source":"window.fixture = 1;"}"#
        func run(_ action: String, _ json: String = #"{"id":"script"}"#) throws -> BrowserManagement.Response {
            try UserscriptManagement.manage(.init(kind: "userscript", action: action, input: json), target: "test", store: store)
        }
        let prepared = try run("prepare", input)
        XCTAssertEqual(prepared.status, "prepared"); XCTAssertEqual(try store.list(target: "test"), [])
        XCTAssertFalse(prepared.result!.contains("window.fixture"))
        XCTAssertEqual(try run("install", input).status, "installed")
        XCTAssertThrowsError(try run("install", input))
        XCTAssertEqual(try run("disable").status, "disabled")
        XCTAssertEqual(try run("update", input.replacingOccurrences(of: "= 1", with: "= 2")).status, "updated")
        XCTAssertFalse(try store.load(target: "test", id: "script").enabled)
        XCTAssertThrowsError(try run("update", input.dropLast() + #", "revision":"stale"}"#))
        XCTAssertEqual(try run("enable").status, "enabled")
        XCTAssertTrue(try run("list", "{}").result!.contains("script"))
        XCTAssertEqual(try run("uninstall").status, "uninstalled")
        XCTAssertThrowsError(try run("describe"))
        XCTAssertThrowsError(try run("install", #"{"sourceFile":"/etc/passwd"}"#))
        XCTAssertThrowsError(try WorkspaceInput.decode(.init(kind: "session", action: "navigate", input: #"{"target":{"id":"other"}}"#), target: "test"))
        _ = try WorkspaceInput.decode(.init(kind: "session", action: "connect", input: "{\"target\":" + WorkspaceInput.targetJSON("test", url: "https://example.com", title: "Page") + "}"), target: "test")
        let injection = try WorkspaceInput.decode(.init(kind: "session", action: "inject", input: #"{"source":"1","options":{"runAt":"document-start"}}"#), target: "test")
        XCTAssertThrowsError(try injection.validateInjection())
    }
}
