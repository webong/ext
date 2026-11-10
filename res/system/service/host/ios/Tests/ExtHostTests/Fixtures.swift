import Foundation

/// The language-neutral vectors of `res/system/testdata`, which the Go, Java and C# tests run too.
/// Set `EXT_SYSTEM_TESTDATA` to use a copy elsewhere.
enum Fixtures {
    static let directory: URL = {
        if let path = ProcessInfo.processInfo.environment["EXT_SYSTEM_TESTDATA"], !path.isEmpty {
            return URL(fileURLWithPath: path)
        }
        // res/system/service/host/ios/Tests/ExtHostTests/Fixtures.swift -> res/system.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<6 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("testdata")
    }()

    static func load<T: Decodable>(_ path: String, as type: T.Type = T.self) throws -> T {
        try JSONDecoder().decode(T.self, from: Data(contentsOf: directory.appendingPathComponent(path)))
    }

    /// The error codes of `codes.json`.
    static func codes() throws -> Set<String> {
        struct File: Decodable { let codes: [String] }
        return Set(try load("codes.json", as: File.self).codes)
    }
}
