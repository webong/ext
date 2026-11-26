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
        Fixtures.warmUpNativeObservers()
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

    func testAccessibilityWireNamesAndTextIsOptIn() throws {
        XCTAssertThrowsError(try AccessibilityOptions(maxElements: 0))
        let now = "1970-01-01T00:16:40.000Z"
        let element = AccessibilityElement(
            id: "accessibility:opaque", parentID: "accessibility:parent", pid: 42,
            processStartID: "42:1", role: "button", identifier: "save",
            bounds: SurfaceBounds(x: 1, y: 2, width: 30, height: 40), actions: ["press"]
        )
        let value = AccessibilitySnapshot(
            platform: "fixture", observedAt: now,
            status: SurfaceCollectionStatus(state: "complete", observedAt: now), elements: [element]
        )
        let data = try JSONEncoder().encode(value)
        let encoded = String(decoding: data, as: UTF8.self)
        XCTAssertTrue(encoded.contains("\"parent_id\""))
        XCTAssertTrue(encoded.contains("\"process_start_id\""))
        XCTAssertFalse(encoded.contains("\"label\""))
        XCTAssertFalse(encoded.contains("\"value\""))
        XCTAssertEqual(try JSONDecoder().decode(AccessibilitySnapshot.self, from: data), value)
    }

    @MainActor
    func testNativePermissionAndAccessibilityObservationDoNotPrompt() throws {
        Fixtures.warmUpNativeObservers()
        let permissions = SystemPermissionObserver.observe()
        XCTAssertFalse(permissions.platform.isEmpty)
        XCTAssertFalse(permissions.accessibility.state.isEmpty)
        XCTAssertFalse(permissions.screenRecording.state.isEmpty)

        let snapshot = try SystemAccessibilityObserver(options: try AccessibilityOptions(maxElements: 64, maxDepth: 8)).observe()
        XCTAssertFalse(snapshot.status.state.isEmpty)
        for element in snapshot.elements {
            XCTAssertTrue(element.id.hasPrefix("accessibility:"))
            XCTAssertNil(element.label)
            XCTAssertNil(element.value)
        }
    }

    func testTimeoutsAreBounded() throws {
        for timeout in [0, -1, 3_601, .infinity, .nan] as [TimeInterval] {
            XCTAssertThrowsError(try SurfaceOptions(timeout: timeout), "surface timeout \(timeout)") { error in
                XCTAssertEqual((error as? SurfaceObservationError)?.code, "invalid")
            }
            XCTAssertThrowsError(try AccessibilityOptions(timeout: timeout), "accessibility timeout \(timeout)") { error in
                XCTAssertEqual((error as? AccessibilityObservationError)?.code, "invalid")
            }
        }
        XCTAssertEqual(try SurfaceOptions().timeout, 5)
        XCTAssertEqual(try AccessibilityOptions().timeout, 5)
    }

    @MainActor
    func testObservationsTimeOut() throws {
        Fixtures.warmUpNativeObservers()
        XCTAssertThrowsError(try SystemSurfaceObserver(options: SurfaceOptions(timeout: 1e-9)).observe()) { error in
            XCTAssertEqual(error as? SurfaceObservationError, .timeout)
            XCTAssertEqual((error as? SurfaceObservationError)?.code, "timeout")
        }
        XCTAssertThrowsError(try SystemAccessibilityObserver(options: AccessibilityOptions(timeout: 1e-9)).observe()) { error in
            XCTAssertEqual(error as? AccessibilityObservationError, .timeout)
            XCTAssertEqual((error as? AccessibilityObservationError)?.code, "timeout")
        }
    }

    func testObservationsAreCanceledWithTheirTask() async throws {
        let task = Task { @MainActor () -> [Error?] in
            while !Task.isCancelled { await Task.yield() }
            var errors: [Error?] = []
            do { _ = try SystemSurfaceObserver().observe(); errors.append(nil) } catch { errors.append(error) }
            do { _ = try SystemAccessibilityObserver().observe(); errors.append(nil) } catch { errors.append(error) }
            return errors
        }
        task.cancel()
        let errors = await task.value
        XCTAssertEqual(errors.first as? SurfaceObservationError, .canceled)
        XCTAssertEqual(errors.last as? AccessibilityObservationError, .canceled)
        XCTAssertEqual((errors.last as? AccessibilityObservationError)?.code, "canceled")
    }
}
