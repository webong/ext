import Foundation
import XCTest

/// The language-neutral vectors of `tests/fixtures/web`, which the Go tests also run. Set
/// `EXT_WEB_TESTDATA` to use a copy elsewhere.
enum Fixtures {
    static let directory: URL = {
        if let path = ProcessInfo.processInfo.environment["EXT_WEB_TESTDATA"] {
            return URL(fileURLWithPath: path)
        }
        // pkg/ext-darwin/web/Tests/ExtWebTests/Fixtures.swift -> src/ctx/web.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<6 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("tests/fixtures/web")
    }()

    static func load<T: Decodable>(_ path: String, as type: T.Type = T.self) throws -> T {
        let url = directory.appendingPathComponent(path)
        let data = try Data(contentsOf: url)
        return try JSONDecoder().decode(T.self, from: data)
    }
}
