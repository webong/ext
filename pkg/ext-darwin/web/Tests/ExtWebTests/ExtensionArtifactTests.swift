import Foundation
import XCTest
@testable import ExtWeb

final class ExtensionArtifactTests: XCTestCase {
    private var root: URL!
    override func setUpWithError() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    }
    override func tearDownWithError() throws { try FileManager.default.removeItem(at: root) }
    private func path(_ name: String) -> URL { root.appendingPathComponent(name) }
    private func fixture(_ name: String) -> URL { Fixtures.directory.appendingPathComponent("extension/" + name) }
    func testSharedFixtures() throws {
        struct Case: Decodable { let file: String, valid: Bool }
        let expected = try Fixtures.load("extension/expected.json", as: ExtensionArtifacts.Description.self)
        for c in try Fixtures.load("extension/cases.json", as: [Case].self) {
            if c.valid { XCTAssertEqual(try ExtensionArtifacts.inspect(fixture(c.file)), expected, c.file) }
            else { XCTAssertThrowsError(try ExtensionArtifacts.inspect(fixture(c.file)), c.file) }
        }
    }
    func testPackageStageAndRevision() throws {
        let original = try ExtensionArtifacts.inspect(fixture("deflated.XPI"))
        let staged = try ExtensionArtifacts.stage(fixture("wrapped.zip"), destination: path("source"), expectedRevision: original.revision)
        XCTAssertEqual(original, staged)
        XCTAssertEqual(try ExtensionArtifacts.checkedSource(path("source"), revision: original.revision), original)
        XCTAssertThrowsError(try ExtensionArtifacts.checkedSource(path("source"), revision: ""))
        XCTAssertEqual(try ExtensionArtifacts.packageArchive(path("source"), output: path("a.zip")), original)
        XCTAssertEqual(try ExtensionArtifacts.packageArchive(path("source"), output: path("b.zip")), original)
        XCTAssertEqual(try Data(contentsOf: path("a.zip")), try Data(contentsOf: path("b.zip")))
        XCTAssertEqual(try ExtensionArtifacts.inspect(path("a.zip")), original)
        XCTAssertEqual(try Data(contentsOf: path("a.zip")), try Data(contentsOf: fixture("mobile.zip")))
        XCTAssertThrowsError(try ExtensionArtifacts.packageArchive(path("source"), output: path("a.zip")))
        XCTAssertThrowsError(try ExtensionArtifacts.stage(path("a.zip"), destination: path("source")))
        XCTAssertThrowsError(try ExtensionArtifacts.packageArchive(path("source"), output: path("source/inside.zip")))
        try Data("changed".utf8).write(to: path("source/scripts/main.js"))
        XCTAssertThrowsError(try ExtensionArtifacts.checkedSource(path("source"), revision: original.revision))
        XCTAssertThrowsError(try ExtensionArtifacts.stage(path("source"), destination: path("stale"), expectedRevision: original.revision))
        XCTAssertFalse(FileManager.default.fileExists(atPath: path("stale").path))
        XCTAssertThrowsError(try ExtensionArtifacts.stage(fixture("long-name.zip"), destination: path("broken")))
        XCTAssertFalse(FileManager.default.fileExists(atPath: path("broken").path))
        XCTAssertThrowsError(try ExtensionArtifacts.copyArtifactExclusive(path("a.zip"), destination: URL(string: "https://example.test/invalid")!))
        try ExtensionArtifacts.copyArtifactExclusive(path("a.zip"), destination: path("copy.zip"))
        XCTAssertEqual(try ExtensionArtifacts.fileSHA256(path("a.zip")), try ExtensionArtifacts.fileSHA256(path("copy.zip")))
        XCTAssertThrowsError(try ExtensionArtifacts.copyArtifactExclusive(path("b.zip"), destination: path("copy.zip")))
        try ExtensionArtifacts.unusedArtifact(path("new/out.ZIP"), extension: ".zip")
        XCTAssertTrue(FileManager.default.fileExists(atPath: path("new").path))
        XCTAssertThrowsError(try ExtensionArtifacts.unusedArtifact(path("a.zip"), extension: ".zip"))
        XCTAssertThrowsError(try ExtensionArtifacts.unusedArtifact(path("bad.txt"), extension: ".zip"))
    }
    func testFilesystemBoundaries() throws {
        _ = try ExtensionArtifacts.stage(fixture("stored.zip"), destination: path("source"))
        try FileManager.default.createSymbolicLink(at: path("alias"), withDestinationURL: path("source"))
        XCTAssertThrowsError(try ExtensionArtifacts.inspect(path("alias")))
        XCTAssertThrowsError(try ExtensionArtifacts.packageArchive(path("source"), output: path("alias/inside.zip")))
        XCTAssertThrowsError(try ExtensionArtifacts.packageArchive(path("source"), output: path("alias/new/deep/inside.zip")))
        try FileManager.default.createSymbolicLink(at: path("dangling.zip"), withDestinationURL: path("absent"))
        XCTAssertThrowsError(try ExtensionArtifacts.packageArchive(path("source"), output: path("dangling.zip")))
        try FileManager.default.createSymbolicLink(at: path("source/link"), withDestinationURL: fixture("stored.zip"))
        XCTAssertThrowsError(try ExtensionArtifacts.inspect(path("source")))
        try FileManager.default.removeItem(at: path("source/link"))
        XCTAssertTrue(FileManager.default.createFile(atPath: path("source/large").path, contents: nil))
        let file = try FileHandle(forWritingTo: path("source/large")); try file.truncate(atOffset: UInt64(17 << 20)); try file.close()
        XCTAssertThrowsError(try ExtensionArtifacts.inspect(path("source")))
    }
    func testZIPEntryLimits() throws {
        let files = (0..<2049).map { ExtensionArtifacts.Item(name: "file\($0)", data: Data()) }
        XCTAssertThrowsError(try ExtensionZIP.decode(ExtensionZIP.encode(files)))
        var archive = try Data(contentsOf: fixture("stored.zip"))
        archive.removeLast()
        XCTAssertThrowsError(try ExtensionZIP.decode(archive))
    }
}
