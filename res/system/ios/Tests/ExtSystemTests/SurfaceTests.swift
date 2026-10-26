import XCTest
@testable import ExtSystem

final class SurfaceTests: XCTestCase {
    func testWireNamesAndBounds() throws {
        XCTAssertThrowsError(try SurfaceOptions(maxDisplays: 0))
        let now = "1970-01-01T00:16:40.000Z"
        let value = SurfaceSnapshot(
            platform: "fixture", coordinateSpace: "fixture-points", observedAt: now,
            displays: [DisplayInfo(id: "display:opaque", bounds: SurfaceBounds(x: -10, y: 0, width: 100, height: 80), scale: 2, primary: true)],
            windows: [WindowInfo(id: "window:opaque", pid: 42, processStartID: "42:1", bounds: SurfaceBounds(x: 1, y: 2, width: 30, height: 40), onScreen: true)],
            displayStatus: SurfaceCollectionStatus(state: "complete", observedAt: now),
            windowStatus: SurfaceCollectionStatus(state: "complete", observedAt: now)
        )
        let data = try JSONEncoder().encode(value)
        let encoded = String(decoding: data, as: UTF8.self)
        XCTAssertTrue(encoded.contains("\"coordinate_space\""))
        XCTAssertTrue(encoded.contains("\"process_start_id\""))
        XCTAssertTrue(encoded.contains("\"display_status\""))
        XCTAssertFalse(encoded.contains("window_title"))
        XCTAssertEqual(try JSONDecoder().decode(SurfaceSnapshot.self, from: data), value)
    }

    @MainActor
    func testNativeObservationHasOpaqueBoundedMetadata() throws {
        let value = try SystemSurfaceObserver(options: SurfaceOptions(maxDisplays: 64, maxWindows: 4096)).observe()
        XCTAssertFalse(value.platform.isEmpty)
        XCTAssertFalse(value.coordinateSpace.isEmpty)
        XCTAssertFalse(value.displayStatus.state.isEmpty)
        XCTAssertFalse(value.windowStatus.state.isEmpty)
        for display in value.displays {
            XCTAssertTrue(display.id.hasPrefix("display:"))
            XCTAssertGreaterThan(display.bounds.width, 0)
            XCTAssertGreaterThan(display.bounds.height, 0)
        }
        for window in value.windows {
            XCTAssertTrue(window.id.hasPrefix("window:"))
            XCTAssertGreaterThan(window.pid, 0)
            XCTAssertFalse(window.processStartID.isEmpty)
            XCTAssertGreaterThan(window.bounds.width, 0)
            XCTAssertGreaterThan(window.bounds.height, 0)
        }
    }
}
