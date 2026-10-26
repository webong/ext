import Foundation
import XCTest
@testable import ExtSystem

final class SystemControlTests: XCTestCase {
    func testAudioPacketBoundsAreTransportFree() {
        XCTAssertTrue(SystemAudioPlayback.validPacket(Data(repeating: 0, count: 4), maxBytes: 8))
        XCTAssertFalse(SystemAudioPlayback.validPacket(Data(), maxBytes: 8))
        XCTAssertFalse(SystemAudioPlayback.validPacket(Data(repeating: 0, count: 3), maxBytes: 8))
        XCTAssertFalse(SystemAudioPlayback.validPacket(Data(repeating: 0, count: 12), maxBytes: 8))
    }

#if os(macOS)
    func testMediaAndSurfaceBounds() throws {
        XCTAssertThrowsError(try MediaCaptureOptions(framesPerSecond: 0))
        XCTAssertThrowsError(try MediaCaptureOptions(framesPerSecond: 61))
        XCTAssertEqual(try MediaCaptureOptions(systemAudio: true, framesPerSecond: 60).framesPerSecond, 60)

        XCTAssertThrowsError(try SurfaceCaptureOptions(maxPixels: 0))
        XCTAssertThrowsError(try SurfaceControlOptions(maxTextLength: 0))
        XCTAssertEqual(try SurfaceControlOptions(maxTextLength: 32).maxTextLength, 32)
        XCTAssertEqual(try SystemAppleEvent.fourCharacterCode("hook"), 0x686f6f6b)
        XCTAssertThrowsError(try SystemAppleEvent.fourCharacterCode("toolong"))
    }

    func testCaptureCarriesBytesInsteadOfRemoteEnvelope() throws {
        let capture = SurfaceCapture(format: "image/png", width: 1, height: 1, data: Data([0]))
        XCTAssertEqual(capture.data, Data([0]))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(capture)) as? [String: Any])
        XCTAssertNil(object["base64Data"])
        XCTAssertNil(object["captureId"])
        XCTAssertNil(object["expiresAt"])
    }
#endif
}
