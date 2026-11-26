import XCTest
@testable import ExtML

final class MLMacOSTests: XCTestCase {
    func testCoreMLProviderTypesExist() {
        // CoreML is available on macOS; the provider type must compile.
        // We only verify the type exists and conforms to MLProvider;
        // constructing a session requires a real compiled .mlmodelc bundle
        // which is not part of the test fixture.
        XCTAssertTrue(CoreMLProvider.self is MLProvider.Type)
    }

    func testMLStoreURLIsFileURL() {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("ml-store-test-\(UUID().uuidString)")
        do {
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            defer { try? FileManager.default.removeItem(at: root) }
            XCTAssertTrue(root.isFileURL)
            _ = try MLStore(rootURL: root)
        } catch {
            XCTFail("MLStore should initialize on macOS: \(error)")
        }
    }
}
