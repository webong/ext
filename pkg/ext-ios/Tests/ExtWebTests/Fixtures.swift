import Foundation
import XCTest

/// The language-neutral vectors of `res/web/testdata`, which the Go tests also run. Set
/// `EXT_WEB_TESTDATA` to use a copy elsewhere.
enum Fixtures {
    static let directory: URL = {
        if let path = ProcessInfo.processInfo.environment["EXT_WEB_TESTDATA"] {
            return URL(fileURLWithPath: path)
        }
        // pkg/ext-ios/Tests/ExtWebTests/Fixtures.swift -> the repository root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("res/web/testdata")
    }()

    static func load<T: Decodable>(_ path: String, as type: T.Type = T.self) throws -> T {
        let url = directory.appendingPathComponent(path)
        let data = try Data(contentsOf: url)
        return try JSONDecoder().decode(T.self, from: data)
    }
}
