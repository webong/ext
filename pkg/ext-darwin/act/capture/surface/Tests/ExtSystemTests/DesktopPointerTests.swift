import XCTest
@testable import ExtSystem

#if os(macOS)
import CoreGraphics

/// Records what would be posted; these tests never move the real pointer.
private final class RecordingEvents: DesktopPointerEvents, @unchecked Sendable {
    let bounds = CGRect(x: -100, y: 50, width: 300, height: 200)
    private let lock = NSLock()
    private var posted: [DesktopPointerEvent] = []
    var failNext = false

    var events: [DesktopPointerEvent] {
        lock.lock(); defer { lock.unlock() }
        return posted
    }

    func displayBounds() throws -> CGRect { bounds }

    func post(_ event: DesktopPointerEvent) throws {
        lock.lock(); defer { lock.unlock() }
        if failNext {
            failNext = false
            throw DesktopPointerError.nativeFailure
        }
        posted.append(event)
    }
}

final class DesktopPointerTests: XCTestCase {
    func testPositionsAreMappedIntoGlobalCoordinatesAndRefusedOutsideTheDisplays() throws {
        let events = RecordingEvents()
        let pointer = SystemDesktopPointer(events: events)
        try pointer.click(at: DesktopPoint(x: 10, y: 20))
        try pointer.drag(from: DesktopPoint(x: 0, y: 0), to: DesktopPoint(x: 299, y: 199), button: .right)
        XCTAssertEqual(events.events, [
            .click(button: .left, x: -90, y: 70),
            .drag(button: .right, x: -100, y: 50, endX: 199, endY: 249),
        ])
        for point in [DesktopPoint(x: 300, y: 0), DesktopPoint(x: 0, y: 200), DesktopPoint(x: -1, y: 0)] {
            XCTAssertThrowsError(try pointer.move(to: point)) {
                XCTAssertEqual($0 as? DesktopPointerError, .outsideDisplays)
            }
        }
        XCTAssertEqual(events.events.count, 2, "a refused position must not post anything")
        XCTAssertThrowsError(try pointer.scroll(at: DesktopPoint(x: 1, y: 1), deltaY: 2_000_000)) {
            XCTAssertEqual($0 as? DesktopPointerError, .invalidInput)
        }
    }

    func testASessionHoldsDragsAndReleases() throws {
        let events = RecordingEvents()
        let session = try SystemDesktopPointer(events: events).openSession()
        try session.move(to: DesktopPoint(x: 5, y: 5))
        try session.down(at: DesktopPoint(x: 5, y: 5))
        try session.move(to: DesktopPoint(x: 50, y: 60))
        try session.up(at: DesktopPoint(x: 50, y: 60))
        XCTAssertEqual(events.events, [
            .move(x: -95, y: 55),
            .down(button: .left, x: -95, y: 55),
            .heldMove(button: .left, x: -50, y: 110),
            .up(button: .left, x: -50, y: 110),
        ])
        try session.close()
        XCTAssertEqual(events.events.count, 4, "closing with nothing held posts nothing")
    }

    func testBadSequencesAreRefusedAndAFailedPressIsNotHeld() throws {
        let events = RecordingEvents()
        let session = try SystemDesktopPointer(events: events).openSession()
        defer { try? session.close() }
        XCTAssertThrowsError(try session.up(at: DesktopPoint(x: 0, y: 0))) {
            XCTAssertEqual($0 as? DesktopPointerError, .notHeld)
        }
        events.failNext = true
        XCTAssertThrowsError(try session.down(at: DesktopPoint(x: 0, y: 0)))
        try session.down(at: DesktopPoint(x: 0, y: 0))
        XCTAssertThrowsError(try session.down(at: DesktopPoint(x: 0, y: 0))) {
            XCTAssertEqual($0 as? DesktopPointerError, .alreadyHeld)
        }
    }

    func testClosingReleasesEveryHeldButtonAtTheLastPosition() throws {
        let events = RecordingEvents()
        let session = try SystemDesktopPointer(events: events).openSession()
        try session.down(at: DesktopPoint(x: 1, y: 2), button: .left)
        try session.down(at: DesktopPoint(x: 1, y: 2), button: .right)
        try session.move(to: DesktopPoint(x: 9, y: 8))
        try session.close()
        XCTAssertEqual(Array(events.events.suffix(2)), [
            .up(button: .right, x: -91, y: 58),
            .up(button: .left, x: -91, y: 58),
        ])
        try session.close()
        XCTAssertThrowsError(try session.move(to: DesktopPoint(x: 0, y: 0))) {
            XCTAssertEqual($0 as? DesktopPointerError, .sessionClosed)
        }
    }

    func testDeallocatingASessionReleasesWhatItHolds() throws {
        let events = RecordingEvents()
        do {
            let session = try SystemDesktopPointer(events: events).openSession()
            try session.down(at: DesktopPoint(x: 3, y: 4))
        }
        XCTAssertEqual(events.events.last, .up(button: .left, x: -97, y: 54))
    }

    func testATimeLimitReleasesWhatTheSessionHolds() throws {
        let events = RecordingEvents()
        let session = try SystemDesktopPointer(events: events).openSession(maxDuration: 0.05)
        try session.down(at: DesktopPoint(x: 3, y: 4))
        let released = expectation(description: "released")
        DispatchQueue.global().async {
            while events.events.count < 2 { usleep(5_000) }
            released.fulfill()
        }
        wait(for: [released], timeout: 2)
        XCTAssertEqual(events.events.last, .up(button: .left, x: -97, y: 54))
        XCTAssertThrowsError(try session.move(to: DesktopPoint(x: 0, y: 0))) {
            XCTAssertEqual($0 as? DesktopPointerError, .sessionClosed)
        }
    }

    func testSessionDurationIsBounded() {
        let pointer = SystemDesktopPointer(events: RecordingEvents())
        for seconds in [0.0, -1.0, 3601.0] {
            XCTAssertThrowsError(try pointer.openSession(maxDuration: seconds))
        }
    }
}
#endif
