import XCTest
@testable import ExtAudio

/// Runs the `playback` cases of `testdata/audio/v1`: the packet rule of a stereo 48 kHz 16-bit sink,
/// which the Go, Java and C# bindings run as well. The other sections are Go's.
final class FixtureTests: XCTestCase {
    private struct Cases: Decodable {
        struct Playback: Decodable { let name: String; let bytes: Int; let maxBytes: Int; let code: String? }
        struct Defaults: Decodable { let playbackPacketBytes: Int }
        let defaults: Defaults
        let playback: [Playback]
    }

    func testPlaybackPackets() throws {
        let cases: Cases = try Fixtures.load("audio/v1/cases.json")
        let codes = try Fixtures.codes()
        XCTAssertFalse(cases.playback.isEmpty)
        for c in cases.playback {
            var got: String?
            do { try SystemAudioPlayback.validate(Data(count: c.bytes), maxBytes: c.maxBytes) }
            catch let error as SystemAudioPlaybackError { got = error.code }
            if let want = c.code { XCTAssertTrue(codes.contains(want), "\(c.name): code \(want) is not in codes.json") }
            XCTAssertEqual(got, c.code, "playback/\(c.name)")
        }
        XCTAssertEqual(3_840, cases.defaults.playbackPacketBytes, "the default packet ceiling")
    }
}
