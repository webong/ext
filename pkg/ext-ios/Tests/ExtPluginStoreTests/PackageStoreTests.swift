import CExtEngine
import ExtPluginStore
import Foundation
import XCTest

/// The package store of the C engine through the Swift binding: the lifecycle, the
/// failures each call has, permissions, limits and the manifest copied as it was. The Go
/// package store is the baseline and pkg/ext-go/cgoengine holds the C store to it; the
/// Rust, Node and Zig bindings run the same scenarios, and this checks that the Swift
/// binding reaches the same behavior.
final class PackageStoreTests: XCTestCase {
    // MARK: Fixtures

    /// A scratch directory that goes away with the test.
    private final class Scratch {
        let base: URL
        init() throws {
            base = FileManager.default.temporaryDirectory.appendingPathComponent("ext-store-test-\(UUID().uuidString)")
            try FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        }
        func path(_ name: String) -> String { base.appendingPathComponent(name).path }
        func url(_ name: String) -> URL { base.appendingPathComponent(name) }
    }

    private func scratch() throws -> Scratch {
        let scratch = try Scratch()
        addTeardownBlock { try? FileManager.default.removeItem(at: scratch.base) }
        return scratch
    }

    private func hex(_ bytes: [UInt8]) -> String { bytes.map { String(format: "%02x", $0) }.joined() }

    private func sha256(_ text: String) -> String {
        var digest = [UInt8](repeating: 0, count: 32)
        let bytes = Array(text.utf8)
        XCTAssertEqual(ext_engine_sha256(bytes, bytes.count, &digest), ext_status(EXT_OK))
        return hex(digest)
    }

    private func directoryDigest(_ path: String) -> String {
        var digest = [UInt8](repeating: 0, count: 32)
        XCTAssertEqual(ext_directory_digest(path, &digest), ext_status(EXT_OK))
        return hex(digest)
    }

    private func write(_ dir: URL, _ relative: String, _ body: String, executable: Bool = false) throws {
        let file = dir.appendingPathComponent(relative)
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data(body.utf8).write(to: file)
        try FileManager.default.setAttributes([.posixPermissions: executable ? 0o700 : 0o600], ofItemAtPath: file.path)
    }

    /// A source package at `dir`: `src/main.py` holding `body`, and with `tree` the tree
    /// artifact `data`. `contract` is what an upgrade may not change. The manifest ends in
    /// blank lines, so a store that re-encoded it would not give the same bytes back.
    @discardableResult
    private func package(_ dir: URL, _ id: String, _ revision: String = "r1", contract: String = "example.read",
                         body: String, tree: Bool = false) throws -> String {
        try? FileManager.default.removeItem(at: dir)
        try write(dir, "src/main.py", body)
        var artifacts = #"{"name":"main","path":"src/main.py","sha256":"\#(sha256(body))"}"#
        if tree {
            try write(dir, "data/x.txt", "ex")
            try write(dir, "data/sub/y.txt", "why")
            try write(dir, "data/run.sh", "#!/bin/sh\n", executable: true)
            artifacts += #",{"name":"data","path":"data","sha256":"\#(directoryDigest(dir.appendingPathComponent("data").path))","kind":"tree"}"#
        }
        let manifest = #"{"apiVersion":"ext.package/v1","descriptor":{"apiVersion":"ext.plugin/v1","identity":{"id":"\#(id)","revision":"\#(revision)"},"contracts":[{"name":"\#(contract)","version":"v1","operations":[{"name":"read"}]}]},"artifacts":[\#(artifacts)]}"#
        try write(dir, "plugin.json", manifest + "\r\n\r\n")
        return dir.path
    }

    private func entries(_ root: URL) -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: root.path)) ?? []).sorted()
    }

    private func mode(_ url: URL) throws -> Int {
        let permissions = try FileManager.default.attributesOfItem(atPath: url.path)[.posixPermissions] as? NSNumber
        return (permissions?.intValue ?? -1) & 0o777
    }

    private func exists(_ url: URL) -> Bool { FileManager.default.fileExists(atPath: url.path) }

    private func read(_ url: URL) throws -> Data { try Data(contentsOf: url) }

    private func text(_ url: URL) throws -> String { String(decoding: try read(url), as: UTF8.self) }

    private func tamper(_ url: URL) throws { try Data("tampered".utf8).write(to: url) }

    private func assertFails<T>(_ expected: PackageStoreError, _ body: @autoclosure () throws -> T,
                                _ message: String = "", file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertThrowsError(try body(), message, file: file, line: line) { error in
            XCTAssertEqual(error as? PackageStoreError, expected, message, file: file, line: line)
        }
    }

    // MARK: Statuses

    func testEveryEngineStatusHasAnError() {
        XCTAssertNil(PackageStoreError(status: ext_status(EXT_OK)))
        XCTAssertEqual(PackageStoreError(status: ext_status(EXT_INVALID)), .invalid)
        XCTAssertEqual(PackageStoreError(status: ext_status(EXT_MISMATCH)), .mismatch)
        XCTAssertEqual(PackageStoreError(status: ext_status(EXT_NOT_FOUND)), .notFound)
        XCTAssertEqual(PackageStoreError(status: ext_status(EXT_EXISTS)), .exists)
        XCTAssertEqual(PackageStoreError(status: ext_status(EXT_MISSING)), .missing)
        XCTAssertEqual(PackageStoreError(status: ext_status(EXT_IO)), .io)
        XCTAssertEqual(PackageStoreError(status: ext_status(EXT_NOMEM)), .engine(ext_status(EXT_NOMEM)))
        // the numbers other languages use for the statuses of a store
        XCTAssertEqual(
            [EXT_INVALID, EXT_MISMATCH, EXT_NOT_FOUND, EXT_EXISTS, EXT_MISSING].map { Int($0) },
            [1, 3, 10, 18, 19])
    }

    // MARK: Lifecycle

    func testAnEmptyStoreHasNothingToFind() throws {
        let scratch = try scratch()
        let store = try PackageStore(root: scratch.path("store"))
        XCTAssertEqual(try store.ids(), [])
        assertFails(.notFound, try store.load("example/a"))
        assertFails(.notFound, try store.remove("example/a"))
        let source = try package(scratch.url("s"), "example/a", body: "one")
        assertFails(.notFound, try store.upgrade(source))
        assertFails(.invalid, try store.load("../bad"))
        assertFails(.invalid, try store.remove("Bad"))
        assertFails(.invalid, try store.install(""))
        XCTAssertFalse(exists(scratch.url("store")), "a failed call made the root")
        assertFails(.invalid, try PackageStore(root: ""))
        assertFails(.invalid, try PackageStore(root: scratch.path("nul\u{0}store")), "a NUL cuts a path short")
        assertFails(.invalid, try store.load("example/a\u{0}b"))
        assertFails(.invalid, try store.install(source + "\u{0}x"))
    }

    func testInstallUpgradeReplaceAndRemove() throws {
        let scratch = try scratch()
        let root = scratch.url("store")
        let store = try PackageStore(root: root.path)

        // install returns plugin.json as the source had it, and never replaces
        let first = try package(scratch.url("s1"), "example/a", "r1", body: "one", tree: true)
        let source = try read(scratch.url("s1/plugin.json"))
        XCTAssertEqual(try store.install(first), source)
        XCTAssertEqual(try read(root.appendingPathComponent("example%2fa/plugin.json")), source)
        assertFails(.exists, try store.install(first))
        XCTAssertEqual(try store.ids(), ["example/a"])
        XCTAssertEqual(try store.load("example/a"), source)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fa")), 0o700)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fa/src/main.py")), 0o600)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fa/data/x.txt")), 0o600)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fa/data/run.sh")), 0o700)

        // upgrade: a new revision of the same id and contracts, in place
        let second = try package(scratch.url("s2"), "example/a", "r2", body: "two", tree: true)
        let renewed = try read(scratch.url("s2/plugin.json"))
        XCTAssertEqual(try store.upgrade(second), renewed)
        XCTAssertEqual(try text(root.appendingPathComponent("example%2fa/src/main.py")), "two")
        XCTAssertEqual(entries(root), ["example%2fa"], "staging or backup left behind")
        assertFails(.invalid, try store.upgrade(second), "same revision")
        let other = try package(scratch.url("s3"), "example/a", "r3", contract: "example.write", body: "three")
        assertFails(.mismatch, try store.upgrade(other), "contracts")
        XCTAssertEqual(try text(root.appendingPathComponent("example%2fa/src/main.py")), "two")

        // replace may change the contracts, and installs what is not there
        try store.replace(other)
        XCTAssertEqual(try text(root.appendingPathComponent("example%2fa/src/main.py")), "three")
        XCTAssertFalse(exists(root.appendingPathComponent("example%2fa/data")), "the tree is gone")
        let bee = try package(scratch.url("s4"), "example/b", body: "bee")
        try store.replace(bee)
        XCTAssertEqual(try store.ids(), ["example/a", "example/b"])

        try store.remove("example/b")
        assertFails(.notFound, try store.remove("example/b"))
        assertFails(.notFound, try store.load("example/b"))
        try store.remove("example/a")
        XCTAssertEqual(entries(root), [])
    }

    func testASourceThatIsNotWhatItsManifestSaysIsNotInstalled() throws {
        let scratch = try scratch()
        let store = try PackageStore(root: scratch.path("store"))
        let dir = scratch.url("s")
        let source = try package(dir, "example/c", body: "cee", tree: true)
        try write(dir, "extra.txt", "unlisted")
        assertFails(.invalid, try store.install(source), "an unlisted file")
        try FileManager.default.removeItem(at: dir.appendingPathComponent("extra.txt"))
        try Data("changed".utf8).write(to: dir.appendingPathComponent("src/main.py"))
        assertFails(.mismatch, try store.install(source), "an artifact that differs")
        try Data("cee".utf8).write(to: dir.appendingPathComponent("src/main.py"))
        try FileManager.default.removeItem(at: dir.appendingPathComponent("data/sub/y.txt"))
        assertFails(.mismatch, try store.install(source), "a tree that differs")
        assertFails(.missing, try store.install(scratch.path("nowhere")), "a source that is not there")
        // a file is not a package source
        assertFails(.invalid, try store.install(dir.appendingPathComponent("plugin.json").path))
        // nothing was left below the root, whatever it was
        XCTAssertEqual(entries(scratch.url("store")), [])
    }

    func testDirectoriesThatAreNotWhatTheirNameSays() throws {
        let scratch = try scratch()
        let root = scratch.url("store")
        let store = try PackageStore(root: root.path)
        try store.install(try package(scratch.url("s"), "example/b", body: "b"))
        try FileManager.default.moveItem(at: root.appendingPathComponent("example%2fb"), to: root.appendingPathComponent("example%2fz"))
        assertFails(.mismatch, try store.load("example/z"))
        assertFails(.mismatch, try store.remove("example/z"))
        try FileManager.default.moveItem(at: root.appendingPathComponent("example%2fz"), to: root.appendingPathComponent("example%2fb"))
        // changed behind the store's back
        try tamper(root.appendingPathComponent("example%2fb/src/main.py"))
        assertFails(.mismatch, try store.load("example/b"))
        // staging directories are skipped, anything else that is not a package is not
        try FileManager.default.createDirectory(at: root.appendingPathComponent(".install-left"), withIntermediateDirectories: false)
        XCTAssertEqual(try store.ids(), ["example/b"])
        try Data("x".utf8).write(to: root.appendingPathComponent("stray"))
        assertFails(.invalid, try store.ids())
        try FileManager.default.removeItem(at: root.appendingPathComponent("stray"))
        try FileManager.default.createDirectory(at: root.appendingPathComponent("Not-A-Package"), withIntermediateDirectories: false)
        assertFails(.invalid, try store.ids())
    }

    func testLimitsBoundAnArtifactAndATree() throws {
        let scratch = try scratch()
        let root = scratch.path("store")
        let big = try package(scratch.url("big"), "example/f", body: "three bytes")
        assertFails(.invalid, try PackageStore(root: root, limits: PackageStoreLimits(maxArtifactBytes: 2)).install(big))
        let tree = try package(scratch.url("tree"), "example/g", body: "ok", tree: true)
        assertFails(.invalid, try PackageStore(root: root, limits: PackageStoreLimits(maxTreeFiles: 2)).install(tree))
        assertFails(.invalid, try PackageStore(root: root, limits: PackageStoreLimits(maxTreeBytes: 4)).install(tree))
        XCTAssertEqual(entries(scratch.url("store")), [])
        try PackageStore(root: root).install(tree)
        XCTAssertEqual(PackageStoreLimits(), PackageStoreLimits(maxArtifactBytes: 0, maxTreeFiles: 0, maxTreeBytes: 0), "zero is the default")
    }

    func testPreserveExecutableKeepsTheExecuteBitOfAFileArtifact() throws {
        let scratch = try scratch()
        let root = scratch.url("store")
        let store = try PackageStore(root: root.path)
        let dir = scratch.url("s")
        let source = try package(dir, "example/e", body: "run me")
        try write(dir, "src/main.py", "run me", executable: true) // the same bytes, executable
        try store.install(source)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fe/src/main.py")), 0o600)
        try store.replace(source, preserveExecutable: true)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fe/src/main.py")), 0o700)
        // upgrade takes the flag too, and only a new revision is an upgrade
        let next = try package(dir, "example/e", "r2", body: "run me")
        try write(dir, "src/main.py", "run me", executable: true)
        try store.upgrade(next)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fe/src/main.py")), 0o600)
        try write(dir, "plugin.json", try text(dir.appendingPathComponent("plugin.json")).replacingOccurrences(of: "\"r2\"", with: "\"r3\""))
        try store.upgrade(next, preserveExecutable: true)
        XCTAssertEqual(try mode(root.appendingPathComponent("example%2fe/src/main.py")), 0o700)
    }

    func testAStoreIsSharedBetweenThreads() throws {
        let scratch = try scratch()
        let store = try PackageStore(root: scratch.path("store"))
        let sources = try (0..<4).map { try package(scratch.url("s\($0)"), "example/t\($0)", body: "t\($0)") }
        let failures = Failures()
        DispatchQueue.concurrentPerform(iterations: sources.count) { index in
            do { try store.install(sources[index]) } catch { failures.add(error) }
        }
        XCTAssertEqual(failures.all.count, 0, "\(failures.all)")
        XCTAssertEqual(try store.ids(), ["example/t0", "example/t1", "example/t2", "example/t3"])
    }

    private final class Failures: @unchecked Sendable {
        private let lock = NSLock()
        private var errors: [Error] = []
        func add(_ error: Error) { lock.lock(); errors.append(error); lock.unlock() }
        var all: [Error] { lock.lock(); defer { lock.unlock() }; return errors }
    }

    // MARK: Reports

    private func json(_ data: Data) throws -> Any { try JSONSerialization.jsonObject(with: data) }

    private func lines(_ data: Data) throws -> [[String: Any]] { try json(data) as! [[String: Any]] }

    private func listing(_ data: Data) throws -> (packages: [[String: Any]], problems: [[String: Any]]) {
        let object = try json(data) as! [String: Any]
        XCTAssertEqual(Set(object.keys), ["packages", "problems"])
        return (object["packages"] as! [[String: Any]], object["problems"] as! [[String: Any]])
    }

    /// The part of a report line that names an entry.
    private func entry(_ line: [String: Any]) -> [String] {
        [line["id"] as! String, line["directory"] as! String, String(describing: line["layer"]!)]
    }

    private func expected(_ id: String, _ directory: URL, _ layer: Int = 0) -> [String] {
        [id, directory.path, String(layer)]
    }

    private func status(_ line: [String: Any]) -> Int32 { Int32(truncating: line["status"] as! NSNumber) }

    @discardableResult
    private func install(_ root: URL, _ scratch: Scratch, _ name: String, _ id: String, _ body: String) throws -> Data {
        try PackageStore(root: root.path).install(try package(scratch.url(name), id, body: body))
    }

    func testReportsCoverEveryEntry() throws {
        let scratch = try scratch()
        let root = scratch.url("store")
        let store = try PackageStore(root: root.path)

        // a missing root is empty, and asking does not make it
        XCTAssertEqual(String(decoding: try store.list(), as: UTF8.self), "[]")
        XCTAssertEqual(String(decoding: try store.verify(), as: UTF8.self), "[]")
        XCTAssertEqual(String(decoding: try store.listUnverified(), as: UTF8.self), #"{"packages":[],"problems":[]}"#)
        XCTAssertFalse(exists(root), "a report made the root")
        // so is a root with nothing in it
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        XCTAssertEqual(String(decoding: try store.list(), as: UTF8.self), "[]")
        XCTAssertEqual(try store.ids(), [])

        let a = try install(root, scratch, "s1", "example/a", "one")
        let b = try install(root, scratch, "s2", "example/b", "two")
        let aDir = root.appendingPathComponent("example%2fa")
        let bDir = root.appendingPathComponent("example%2fb")

        let listed = try lines(try store.list())
        XCTAssertEqual(listed.map(entry), [expected("example/a", aDir), expected("example/b", bDir)])
        for (line, manifest) in zip(listed, [a, b]) {
            XCTAssertEqual(line["manifest"] as? NSDictionary, try json(manifest) as? NSDictionary, "the manifest is plugin.json as a value")
        }
        XCTAssertEqual(
            String(decoding: try store.verify(), as: UTF8.self),
            #"[{"id":"example/a","directory":\#(try quoted(aDir.path)),"layer":0,"status":0},"#
                + #"{"id":"example/b","directory":\#(try quoted(bDir.path)),"layer":0,"status":0}]"#)

        // verify reports a bad entry and goes on; list fails closed
        try tamper(bDir.appendingPathComponent("src/main.py"))
        let checks = try lines(try store.verify())
        XCTAssertEqual(checks.map(entry), [expected("example/a", aDir), expected("example/b", bDir)])
        XCTAssertEqual(checks.map(status), [0, Int32(EXT_MISMATCH)])
        XCTAssertEqual(PackageStoreError(status: status(checks[1])), .mismatch)
        assertFails(.mismatch, try store.list())
        // the unverified listing reads no artifact, so it still has both
        var unverified = try listing(try store.listUnverified())
        XCTAssertEqual(unverified.packages.map(entry), [expected("example/a", aDir), expected("example/b", bDir)])
        XCTAssertEqual(unverified.problems.count, 0)

        // an entry that is not a package is a problem there, an error in list, and a status
        // in verify; staging directories are skipped
        try Data("x".utf8).write(to: root.appendingPathComponent("stray"))
        try FileManager.default.createDirectory(at: root.appendingPathComponent(".install-left"), withIntermediateDirectories: false)
        assertFails(.invalid, try store.list())
        unverified = try listing(try store.listUnverified())
        XCTAssertEqual(unverified.packages.map(entry), [expected("example/a", aDir), expected("example/b", bDir)])
        XCTAssertEqual(unverified.problems.map(entry), [expected("stray", root.appendingPathComponent("stray"))])
        XCTAssertEqual(unverified.problems.map(status), [Int32(EXT_INVALID)])
        let strays = try lines(try store.verify())
        XCTAssertEqual(strays.map { $0["id"] as! String }, ["example/a", "example/b", "stray"])
        XCTAssertEqual(strays.map(status), [0, Int32(EXT_MISMATCH), Int32(EXT_INVALID)])
        XCTAssertFalse(String(decoding: try store.verify(), as: UTF8.self).contains("install-left"))
    }

    /// A path as the engine's JSON writes it.
    private func quoted(_ path: String) throws -> String {
        let data = try JSONSerialization.data(withJSONObject: [path], options: [.withoutEscapingSlashes])
        let array = String(decoding: data, as: UTF8.self)
        return String(array.dropFirst().dropLast())
    }

    // MARK: Layered view

    func testALayeredViewServesTheHighestRootAndNeverWrites() throws {
        let scratch = try scratch()
        let high = scratch.url("user")
        let low = scratch.url("system")
        let kept = try install(high, scratch, "s1", "example/a", "high")
        try install(low, scratch, "s2", "example/a", "low")
        let onlyLow = try install(low, scratch, "s3", "example/c", "cee")
        let absent = scratch.url("absent")
        let view = try LayeredPackageStore(roots: [high.path, low.path, absent.path])

        XCTAssertEqual(try view.load("example/a"), ServedPackage(manifest: kept, layer: 0))
        XCTAssertEqual(try view.load("example/c"), ServedPackage(manifest: onlyLow, layer: 1))
        assertFails(.notFound, try view.load("example/none"))
        assertFails(.invalid, try view.load("../bad"))
        let listed = try lines(try view.list())
        XCTAssertEqual(
            listed.map(entry),
            [expected("example/a", high.appendingPathComponent("example%2fa"), 0),
             expected("example/c", low.appendingPathComponent("example%2fc"), 1)],
            "one line per id, from the root that serves it")
        XCTAssertFalse(exists(absent), "the view made a root")

        // a shadowed copy is never read
        try tamper(low.appendingPathComponent("example%2fa/src/main.py"))
        XCTAssertNoThrow(try view.load("example/a"))
        XCTAssertNoThrow(try view.list())
        XCTAssertEqual(try lines(try view.verify()).map(status), [0, 0])

        // a damaged winner fails closed instead of falling back to the copy below
        try tamper(high.appendingPathComponent("example%2fa/src/main.py"))
        assertFails(.mismatch, try view.load("example/a"))
        assertFails(.mismatch, try view.list())
        let checks = try lines(try view.verify())
        XCTAssertEqual(checks.map(entry), listed.map(entry))
        XCTAssertEqual(checks.map(status), [Int32(EXT_MISMATCH), 0])
        let unverified = try listing(try view.listUnverified())
        XCTAssertEqual(unverified.packages.map(entry), listed.map(entry))
        XCTAssertEqual(unverified.problems.count, 0)
        XCTAssertFalse(exists(absent), "the view made a root")
    }

    func testALayeredViewReadsEachRootAsAStoreDoes() throws {
        let scratch = try scratch()
        let high = scratch.url("user")
        let low = scratch.url("system")
        try install(high, scratch, "s1", "example/a", "high")
        try install(low, scratch, "s2", "example/b", "low")
        // a lower root with junk: a stray file is a problem, and the others are still served
        try Data("x".utf8).write(to: low.appendingPathComponent("stray"))
        let view = try LayeredPackageStore(roots: [high.path, low.path])
        XCTAssertEqual(try view.load("example/a").layer, 0)
        XCTAssertEqual(try view.load("example/b").layer, 1)
        let unverified = try listing(try view.listUnverified())
        XCTAssertEqual(unverified.packages.map { $0["id"] as! String }, ["example/a", "example/b"])
        XCTAssertEqual(unverified.problems.map(entry), [expected("stray", low.appendingPathComponent("stray"), 1)])
        assertFails(.invalid, try view.list())
        // a view over one root is that root
        let one = try LayeredPackageStore(roots: [high.path])
        XCTAssertEqual(try one.load("example/a").layer, 0)
        assertFails(.notFound, try one.load("example/b"))
    }

    func testALayeredViewRefusesWhatIsNotASetOfRoots() throws {
        let scratch = try scratch()
        let one = scratch.path("one")
        assertFails(.invalid, try LayeredPackageStore(roots: []), "no root")
        assertFails(.invalid, try LayeredPackageStore(roots: [""]), "an empty root")
        assertFails(.invalid, try LayeredPackageStore(roots: [one, one]), "a repeated root")
        assertFails(.invalid, try LayeredPackageStore(roots: [one, one + "/./"]), "the same root spelled differently")
        assertFails(.invalid, try LayeredPackageStore(roots: [one, scratch.path("two") + "\u{0}x"]), "a NUL cuts a path short")
        XCTAssertNoThrow(try LayeredPackageStore(roots: [one, scratch.path("two")]))
        XCTAssertFalse(exists(scratch.url("one")))
        XCTAssertFalse(exists(scratch.url("two")))
    }

    func testLimitsBoundWhatIsCopiedAndNotWhatIsRead() throws {
        let scratch = try scratch()
        let root = scratch.url("store")
        try install(root, scratch, "s1", "example/a", "three bytes")
        let limits = PackageStoreLimits(maxArtifactBytes: 2, maxTreeFiles: 1, maxTreeBytes: 1)
        XCTAssertEqual(try PackageStore(root: root.path, limits: limits).load("example/a").isEmpty, false)
        XCTAssertEqual(try PackageStore(root: root.path, limits: limits).ids(), ["example/a"])
        XCTAssertEqual(try lines(try PackageStore(root: root.path, limits: limits).verify()).map(status), [0])
        XCTAssertEqual(try LayeredPackageStore(roots: [root.path], limits: limits).load("example/a").layer, 0)
    }
}
