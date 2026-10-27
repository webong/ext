import XCTest
@testable import ExtML

final class MLTypesTests: XCTestCase {
    func testManifestValidation() {
        let manifest = MLManifest(apiVersion: mlManifestAPIVersion, name: "example-model",
                                  version: "v1.2.3", provider: "coreml",
                                  capabilities: ["image.classify"],
                                  artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                         sha256: MLDigest.sha256(Data("model bytes".utf8)))])
        XCTAssertNoThrow(try manifest.validate())

        // Duplicate capability rejected.
        let duplicate = MLManifest(apiVersion: mlManifestAPIVersion, name: "example-model",
                                   version: "v1.2.3", provider: "coreml",
                                   capabilities: ["image.classify", "image.classify"],
                                   artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                          sha256: MLDigest.sha256(Data("x".utf8)))])
        XCTAssertThrowsError(try duplicate.validate())

        // Bad apiVersion rejected.
        let badAPI = MLManifest(apiVersion: "content.model/v2", name: "example-model",
                                version: "v1.2.3", provider: "coreml",
                                capabilities: ["image.classify"],
                                artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                       sha256: MLDigest.sha256(Data("x".utf8)))])
        XCTAssertThrowsError(try badAPI.validate())

        // Empty name rejected.
        let emptyName = MLManifest(apiVersion: mlManifestAPIVersion, name: "",
                                   version: "v1.2.3", provider: "coreml",
                                   capabilities: ["image.classify"],
                                   artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                          sha256: MLDigest.sha256(Data("x".utf8)))])
        XCTAssertThrowsError(try emptyName.validate())
    }

    func testArtifactValidation() {
        let digest = MLDigest.sha256(Data("data".utf8))
        let artifact = MLArtifact(role: "model", file: "model.mlmodelc", sha256: digest)
        let manifest = MLManifest(apiVersion: mlManifestAPIVersion, name: "example-model",
                                  version: "v1.2.3", provider: "coreml",
                                  capabilities: [], artifacts: [artifact])
        XCTAssertNoThrow(try manifest.validate())

        // Bad digest length rejected.
        let badManifest = MLManifest(apiVersion: mlManifestAPIVersion, name: "example-model",
                                     version: "v1.2.3", provider: "coreml",
                                     capabilities: [],
                                     artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                            sha256: "not64chars")])
        XCTAssertThrowsError(try badManifest.validate())

        // Non-hex rejected.
        let nonHexManifest = MLManifest(apiVersion: mlManifestAPIVersion, name: "example-model",
                                        version: "v1.2.3", provider: "coreml",
                                        capabilities: [],
                                        artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                               sha256: String(repeating: "z", count: 64))])
        XCTAssertThrowsError(try nonHexManifest.validate())
    }

    func testReference() {
        let ref = MLReference(provider: "coreml", name: "example-model", version: "v1.2.3")
        XCTAssertEqual(ref.stringValue, "coreml/example-model@v1.2.3")
    }

    func testTensorValidation() {
        let data = Data([Float](repeating: 1.0, count: 12).withUnsafeBytes { Data($0) })
        let tensor = MLTensor(shape: [1, 3, 2, 2], data: data, format: "float32")
        XCTAssertNoThrow(try MLTensorValidation.validate(tensor))

        // Bad format rejected.
        let bad = MLTensor(shape: [1, 2], data: data, format: "float64")
        XCTAssertThrowsError(try MLTensorValidation.validate(bad))

        // Wrong data length rejected.
        let short = MLTensor(shape: [2], data: Data([UInt8]([1, 2, 3])), format: "float32")
        XCTAssertThrowsError(try MLTensorValidation.validate(short))
    }
}

final class MLStoreTests: XCTestCase {
    func testStoreRoundTrip() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("ml-store-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }

        let store = try MLStore(rootURL: root)
        let manifest = MLManifest(apiVersion: mlManifestAPIVersion, name: "example-model",
                                  version: "v1.2.3", provider: "coreml",
                                  capabilities: ["image.classify"],
                                  artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                         sha256: MLDigest.sha256(Data("model bytes".utf8)))])
        let installed = try store.install(manifest: manifest,
                                          files: ["model.mlmodelc": Data("model bytes".utf8)])
        XCTAssertEqual(installed.manifest.name, "example-model")

        // Resolve re-verifies digests.
        let ref = MLReference(provider: "coreml", name: "example-model", version: "v1.2.3")
        let resolved = try store.resolve(reference: ref)
        XCTAssertEqual(resolved.directory, installed.directory)

        // List returns the model.
        let list = try store.list()
        XCTAssertEqual(list.count, 1)
        XCTAssertEqual(list[0].manifest.name, "example-model")

        // Remove deletes it.
        try store.remove(reference: ref)
        XCTAssertThrowsError(try store.resolve(reference: ref))
        XCTAssertTrue(try store.list().isEmpty)
    }

    func testStoreRejectsDigestMismatch() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("ml-store-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }

        let store = try MLStore(rootURL: root)
        let manifest = MLManifest(apiVersion: mlManifestAPIVersion, name: "example-model",
                                  version: "v1.2.3", provider: "coreml",
                                  capabilities: [],
                                  artifacts: [MLArtifact(role: "model", file: "model.mlmodelc",
                                                         sha256: MLDigest.sha256(Data("model".utf8)))])
        XCTAssertThrowsError(try store.install(manifest: manifest,
                                               files: ["model.mlmodelc": Data("tampered".utf8)]))
    }
}
