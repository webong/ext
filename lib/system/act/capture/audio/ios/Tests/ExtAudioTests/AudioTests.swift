import Foundation
import XCTest
@testable import ExtAudio

final class AudioTests: XCTestCase {
    func testAudioPacketBoundsAreTransportFree() {
        XCTAssertNoThrow(try SystemAudioPlayback.validate(Data(repeating: 0, count: 4), maxBytes: 8))
        XCTAssertThrowsError(try SystemAudioPlayback.validate(Data(), maxBytes: 8))
        XCTAssertThrowsError(try SystemAudioPlayback.validate(Data(repeating: 0, count: 3), maxBytes: 8))
        XCTAssertThrowsError(try SystemAudioPlayback.validate(Data(repeating: 0, count: 12), maxBytes: 8))
    }
}
