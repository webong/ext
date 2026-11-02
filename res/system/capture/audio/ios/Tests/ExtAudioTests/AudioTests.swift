import Foundation
import XCTest
@testable import ExtAudio

final class AudioTests: XCTestCase {
    func testAudioPacketBoundsAreTransportFree() {
        XCTAssertTrue(SystemAudioPlayback.validPacket(Data(repeating: 0, count: 4), maxBytes: 8))
        XCTAssertFalse(SystemAudioPlayback.validPacket(Data(), maxBytes: 8))
        XCTAssertFalse(SystemAudioPlayback.validPacket(Data(repeating: 0, count: 3), maxBytes: 8))
        XCTAssertFalse(SystemAudioPlayback.validPacket(Data(repeating: 0, count: 12), maxBytes: 8))
    }
}
