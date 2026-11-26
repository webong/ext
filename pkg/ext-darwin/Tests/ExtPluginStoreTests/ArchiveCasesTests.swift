import ExtPluginStore
import Foundation
import XCTest

/// The archive fixtures every store runs, in every language: lib/plugin/testdata/v1/archive-cases.json
/// with the archives beside it, through `PackageStore.installArchive`, `receipt` and `remove`. They are
/// outside this package, so the test skips when the package is used without the repository.
final class ArchiveCasesTests: XCTestCase {
    private static var fixtures: URL {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("lib/plugin/testdata/v1")
    }

    private func status(_ body: () throws -> Void) -> String {
        do {
            try body()
            return "ok"
        } catch let error as PackageStoreError {
            switch error {
            case .invalid: return "invalid"
            case .denied: return "denied"
            case .mismatch: return "mismatch"
            case .unsupported: return "unsupported"
            case .notFound: return "not_found"
            case .exists: return "exists"
            case .missing: return "missing"
            case .io: return "io"
            case .engine(let status): return "engine \(status)"
            }
        } catch {
            return "failed: \(error)"
        }
    }

    private func revision(_ manifest: Data) -> String? {
        let object = try? JSONSerialization.jsonObject(with: manifest) as? [String: Any]
        let identity = object?["identity"] as? [String: Any]
        return identity?["revision"] as? String
    }

    func testEveryArchiveCase() throws {
        let file = Self.fixtures.appendingPathComponent("archive-cases.json")
        guard FileManager.default.fileExists(atPath: file.path) else {
            throw XCTSkip("the shared fixtures are not here: \(file.path)")
        }
        let document = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any])
        let cases = try XCTUnwrap(document["cases"] as? [[String: Any]])
        XCTAssertEqual(cases.count, 71, "the fixture changed size; check this test still covers every step")
        let escape = URL(fileURLWithPath: "/tmp/ext-archive-escape")
        try? FileManager.default.removeItem(at: escape)

        for testCase in cases {
            let name = try XCTUnwrap(testCase["name"] as? String)
            let base = FileManager.default.temporaryDirectory.appendingPathComponent("ext-archive-\(UUID().uuidString)")
            defer { try? FileManager.default.removeItem(at: base) }
            let root = base.appendingPathComponent("store")
            let raw = testCase["limits"] as? [String: Any] ?? [:]
            func limit(_ key: String) -> UInt64 { (raw[key] as? NSNumber)?.uint64Value ?? 0 }
            let limits = PackageStoreLimits(maxArtifactBytes: limit("maxArtifactBytes"),
                                            maxTreeFiles: UInt32(limit("maxTreeFiles")),
                                            maxTreeBytes: limit("maxTreeBytes"),
                                            maxArchiveBytes: limit("maxArchiveBytes"))
            let store = try PackageStore(root: root.path, limits: limits)
            let steps = try XCTUnwrap(testCase["steps"] as? [[String: Any]])

            for (index, step) in steps.enumerated() {
                let label = "\(name) step \(index)"
                let expected = step["status"] as? String ?? "ok"
                var got: String
                if let archive = step["install"] as? String {
                    var manifest = Data()
                    got = status {
                        manifest = try store.installArchive(Self.fixtures.appendingPathComponent(archive).path,
                                                            sha256: step["sha256"] as? String ?? "",
                                                            id: step["id"] as? String)
                    }
                    if got == "ok", let want = step["revision"] as? String {
                        XCTAssertEqual(revision(manifest), want, label)
                    }
                } else if let id = step["receipt"] as? String {
                    var receipt: ArchiveReceipt?
                    got = status { receipt = try store.receipt(id) }
                    if let receipt {
                        XCTAssertEqual(receipt.id, id, label)
                        if let want = step["revision"] as? String { XCTAssertEqual(receipt.revision, want, label) }
                        if let want = step["sha256"] as? String { XCTAssertEqual(receipt.artifactSHA256, want, label) }
                    }
                } else if let id = step["remove"] as? String {
                    got = status { try store.remove(id) }
                } else if let id = step["damage"] as? String, let path = step["path"] as? String {
                    let target = root.appendingPathComponent(id.replacingOccurrences(of: "/", with: "%2f"))
                        .appendingPathComponent(path)
                    got = status { try Data("damaged".utf8).write(to: target) }
                } else {
                    XCTFail("\(label): a step this test does not know: \(step)")
                    continue
                }
                XCTAssertEqual(got, expected, label)
            }

            // Nothing is left under the root but package directories and the receipts.
            let left = (try? FileManager.default.contentsOfDirectory(atPath: root.path)) ?? []
            for entry in left where entry != ".receipts" {
                let manifest = root.appendingPathComponent(entry).appendingPathComponent("plugin.json")
                XCTAssertTrue(FileManager.default.fileExists(atPath: manifest.path), "\(name): left \(entry) under the root")
            }
            XCTAssertFalse(FileManager.default.fileExists(atPath: escape.path), "\(name): wrote outside the store")
        }
    }

    func testAReceiptIsTheLastArchiveInstall() throws {
        let fixtures = Self.fixtures.appendingPathComponent("archives")
        guard FileManager.default.fileExists(atPath: fixtures.path) else { throw XCTSkip("the shared fixtures are not here") }
        let base = FileManager.default.temporaryDirectory.appendingPathComponent("ext-archive-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: base) }
        let store = try PackageStore(root: base.appendingPathComponent("store").path)
        let digest = "7adbf798b3e126b9c84ad914d47a581be48fc1216afcb08b84028e62f0e16020"
        XCTAssertThrowsError(try store.receipt("example/archive")) { XCTAssertEqual($0 as? PackageStoreError, .notFound) }
        try store.installArchive(fixtures.appendingPathComponent("tar-gz-top.bin").path, sha256: digest.uppercased())
        XCTAssertEqual(try store.receipt("example/archive"),
                       ArchiveReceipt(id: "example/archive", revision: "r1", artifactSHA256: digest))
        XCTAssertThrowsError(try store.receipt("Bad")) { XCTAssertEqual($0 as? PackageStoreError, .invalid) }
        XCTAssertThrowsError(try store.installArchive(fixtures.appendingPathComponent("absent.bin").path, sha256: String(repeating: "0", count: 64))) {
            XCTAssertEqual($0 as? PackageStoreError, .missing)
        }
        XCTAssertThrowsError(try store.installArchive(fixtures.appendingPathComponent("tar-gz-top.bin").path + "\u{0}x", sha256: digest)) {
            XCTAssertEqual($0 as? PackageStoreError, .invalid, "a NUL cuts a path short")
        }
    }
}
