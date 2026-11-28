import Foundation
#if os(iOS)
@testable import ExtSystem
#endif

/// The language-neutral vectors of `res/system/testdata`, which the Go, Java and C# tests run too.
/// Set `EXT_SYSTEM_TESTDATA` to use a copy elsewhere.
enum Fixtures {
    static let directory: URL = {
        if let path = ProcessInfo.processInfo.environment["EXT_SYSTEM_TESTDATA"], !path.isEmpty {
            return URL(fileURLWithPath: path)
        }
        // pkg/ext-darwin/act/capture/surface/Tests/ExtSystemTests/Fixtures.swift -> repository root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<8 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("res/system/testdata")
    }()

    static func load<T: Decodable>(_ path: String, as type: T.Type = T.self) throws -> T {
        try JSONDecoder().decode(T.self, from: Data(contentsOf: directory.appendingPathComponent(path)))
    }

    /// Brings UIKit up once, before a test bounds a native observation. In a test bundle without an
    /// app host the first UIKit call (the scene and screen lookups of the surface and accessibility
    /// observers) waits for the simulator's display services, and on a cold CI runner it has taken
    /// 30 to 115 seconds, where an app's own UIKit is already running. An observer cannot interrupt
    /// a synchronous UIKit call, so that wait would surface as `timeout` under the default
    /// five-second bound. The observations the tests check still run under that bound; only this
    /// warm-up runs under the longest one. Nothing to do on macOS.
    @MainActor static func warmUpNativeObservers() {
#if os(iOS)
        _ = nativeObserversReady
#endif
    }

#if os(iOS)
    @MainActor private static let nativeObserversReady: Void = {
        _ = try? SystemSurfaceObserver(options: SurfaceOptions(timeout: ObservationBound.maximumTimeout)).observe()
    }()
#endif

    /// The error codes of `codes.json`.
    static func codes() throws -> Set<String> {
        struct File: Decodable { let codes: [String] }
        return Set(try load("codes.json", as: File.self).codes)
    }
}
