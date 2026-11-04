import Foundation

#if os(macOS)
import ApplicationServices
import CoreGraphics

public enum DesktopPointerError: Error, Equatable {
    case permissionDenied
    case outsideDisplays
    case alreadyHeld
    case notHeld
    case sessionClosed
    case invalidInput
    case nativeFailure
}

public enum DesktopPointerButton: String, Sendable {
    case left, right, middle
}

/// A position in desktop coordinates: the top-left corner of all displays combined is (0, 0), so
/// every display is addressable with non-negative values.
public struct DesktopPoint: Equatable, Sendable {
    public let x: Int
    public let y: Int

    public init(x: Int, y: Int) {
        self.x = x
        self.y = y
    }
}

/// One pointer event in the operating system's global display coordinates.
public enum DesktopPointerEvent: Equatable, Sendable {
    case move(x: Int, y: Int)
    case click(button: DesktopPointerButton, x: Int, y: Int)
    case drag(button: DesktopPointerButton, x: Int, y: Int, endX: Int, endY: Int)
    case scroll(x: Int, y: Int, deltaX: Int, deltaY: Int)
    case down(button: DesktopPointerButton, x: Int, y: Int)
    case up(button: DesktopPointerButton, x: Int, y: Int)
    case heldMove(button: DesktopPointerButton, x: Int, y: Int)
}

/// The operating-system seam of SystemDesktopPointer.
public protocol DesktopPointerEvents: Sendable {
    /// The union of the active displays in global coordinates.
    func displayBounds() throws -> CGRect
    func post(_ event: DesktopPointerEvent) throws
}

/// Posts pointer events to the whole desktop through the HID tap. macOS drops them unless the
/// process has the Accessibility permission, which this type never requests or prompts for.
public struct SystemDesktopPointerEvents: DesktopPointerEvents {
    public init() {}

    public func displayBounds() throws -> CGRect {
        var displays = [CGDirectDisplayID](repeating: 0, count: 16)
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(16, &displays, &count) == .success, count > 0 else {
            throw DesktopPointerError.nativeFailure
        }
        let bounds = displays.prefix(Int(count)).map { CGDisplayBounds($0) }.reduce(CGRect.null) { $0.union($1) }
        guard !bounds.isNull, !bounds.isEmpty else { throw DesktopPointerError.nativeFailure }
        return bounds
    }

    public func post(_ event: DesktopPointerEvent) throws {
        guard AXIsProcessTrusted() else { throw DesktopPointerError.permissionDenied }
        switch event {
        case let .move(x, y):
            try mouse(.mouseMoved, .left, x, y)
        case let .click(button, x, y):
            try mouse(Self.down(button), button, x, y)
            try mouse(Self.up(button), button, x, y)
        case let .drag(button, x, y, endX, endY):
            try mouse(Self.down(button), button, x, y)
            try mouse(Self.dragged(button), button, endX, endY)
            try mouse(Self.up(button), button, endX, endY)
        case let .scroll(x, y, deltaX, deltaY):
            guard let wheel = CGEvent(
                scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 2,
                wheel1: Int32(clamping: deltaY), wheel2: Int32(clamping: deltaX), wheel3: 0
            ) else { throw DesktopPointerError.nativeFailure }
            wheel.location = CGPoint(x: x, y: y)
            wheel.post(tap: .cghidEventTap)
        case let .down(button, x, y):
            try mouse(Self.down(button), button, x, y)
        case let .up(button, x, y):
            try mouse(Self.up(button), button, x, y)
        case let .heldMove(button, x, y):
            try mouse(Self.dragged(button), button, x, y)
        }
    }

    private func mouse(_ type: CGEventType, _ button: DesktopPointerButton, _ x: Int, _ y: Int) throws {
        guard let event = CGEvent(
            mouseEventSource: nil, mouseType: type,
            mouseCursorPosition: CGPoint(x: x, y: y), mouseButton: Self.native(button)
        ) else { throw DesktopPointerError.nativeFailure }
        event.post(tap: .cghidEventTap)
    }

    private static func native(_ button: DesktopPointerButton) -> CGMouseButton {
        switch button {
        case .left: return .left
        case .right: return .right
        case .middle: return .center
        }
    }

    private static func down(_ button: DesktopPointerButton) -> CGEventType {
        switch button {
        case .left: return .leftMouseDown
        case .right: return .rightMouseDown
        case .middle: return .otherMouseDown
        }
    }

    private static func up(_ button: DesktopPointerButton) -> CGEventType {
        switch button {
        case .left: return .leftMouseUp
        case .right: return .rightMouseUp
        case .middle: return .otherMouseUp
        }
    }

    private static func dragged(_ button: DesktopPointerButton) -> CGEventType {
        switch button {
        case .left: return .leftMouseDragged
        case .right: return .rightMouseDragged
        case .middle: return .otherMouseDragged
        }
    }
}

/// Pointer input for the whole desktop. Each call is one atomic operation; buttons can only be
/// held across calls inside a SystemPointerSession, which always releases them.
public final class SystemDesktopPointer: @unchecked Sendable {
    private let events: DesktopPointerEvents

    public init(events: DesktopPointerEvents = SystemDesktopPointerEvents()) {
        self.events = events
    }

    public func move(to point: DesktopPoint) throws {
        let (x, y) = try global(point)
        try events.post(.move(x: x, y: y))
    }

    public func click(at point: DesktopPoint, button: DesktopPointerButton = .left) throws {
        let (x, y) = try global(point)
        try events.post(.click(button: button, x: x, y: y))
    }

    public func drag(from start: DesktopPoint, to end: DesktopPoint, button: DesktopPointerButton = .left) throws {
        let (x, y) = try global(start)
        let (endX, endY) = try global(end)
        try events.post(.drag(button: button, x: x, y: y, endX: endX, endY: endY))
    }

    public func scroll(at point: DesktopPoint, deltaX: Int = 0, deltaY: Int) throws {
        guard abs(deltaX) <= 1_000_000, abs(deltaY) <= 1_000_000 else { throw DesktopPointerError.invalidInput }
        let (x, y) = try global(point)
        try events.post(.scroll(x: x, y: y, deltaX: deltaX, deltaY: deltaY))
    }

    /// Starts a session in which buttons can be held. It ends when it is closed, deallocated, or
    /// after `maxDuration` seconds (at most one hour), and each way releases every held button.
    public func openSession(maxDuration: TimeInterval = 300) throws -> SystemPointerSession {
        guard maxDuration > 0, maxDuration <= 3600 else { throw DesktopPointerError.invalidInput }
        return SystemPointerSession(pointer: self, maxDuration: maxDuration)
    }

    func global(_ point: DesktopPoint) throws -> (Int, Int) {
        let bounds = try events.displayBounds()
        guard point.x >= 0, point.y >= 0, Double(point.x) < bounds.width, Double(point.y) < bounds.height else {
            throw DesktopPointerError.outsideDisplays
        }
        return (Int(bounds.origin.x) + point.x, Int(bounds.origin.y) + point.y)
    }

    func post(_ event: DesktopPointerEvent) throws {
        try events.post(event)
    }
}

/// A span of time in which pointer buttons can be held. Closing it, deallocating it and the end
/// of its time limit each release every held button at the last position, so no button is left
/// down. It is safe to use from several threads; calls are serialized.
public final class SystemPointerSession: @unchecked Sendable {
    private let pointer: SystemDesktopPointer
    private let lock = NSLock()
    private var held: [DesktopPointerButton] = []
    private var last = (x: 0, y: 0)
    private var closed = false
    private var timer: DispatchSourceTimer?

    init(pointer: SystemDesktopPointer, maxDuration: TimeInterval) {
        self.pointer = pointer
        let timer = DispatchSource.makeTimerSource(queue: DispatchQueue.global(qos: .userInitiated))
        timer.schedule(deadline: .now() + maxDuration)
        timer.setEventHandler { [weak self] in try? self?.close() }
        timer.resume()
        self.timer = timer
    }

    deinit {
        try? close()
    }

    public func down(at point: DesktopPoint, button: DesktopPointerButton = .left) throws {
        try perform(point) { x, y in
            guard !held.contains(button) else { throw DesktopPointerError.alreadyHeld }
            try pointer.post(.down(button: button, x: x, y: y))
            held.append(button)
        }
    }

    /// Moves the pointer; while a button is held this drags with it.
    public func move(to point: DesktopPoint) throws {
        try perform(point) { x, y in
            if let button = held.first {
                try pointer.post(.heldMove(button: button, x: x, y: y))
            } else {
                try pointer.post(.move(x: x, y: y))
            }
        }
    }

    public func up(at point: DesktopPoint, button: DesktopPointerButton = .left) throws {
        try perform(point) { x, y in
            guard let index = held.lastIndex(of: button) else { throw DesktopPointerError.notHeld }
            try pointer.post(.up(button: button, x: x, y: y))
            held.remove(at: index)
        }
    }

    /// Ends the session, releasing every held button. Closing twice is harmless.
    public func close() throws {
        lock.lock()
        defer { lock.unlock() }
        guard !closed else { return }
        closed = true
        timer?.cancel()
        timer = nil
        var failure: Error?
        for button in held.reversed() {
            do {
                try pointer.post(.up(button: button, x: last.x, y: last.y))
            } catch {
                failure = failure ?? error
            }
        }
        held.removeAll()
        if let failure { throw failure }
    }

    private func perform(_ point: DesktopPoint, _ operation: (Int, Int) throws -> Void) throws {
        lock.lock()
        defer { lock.unlock() }
        guard !closed else { throw DesktopPointerError.sessionClosed }
        let (x, y) = try pointer.global(point)
        try operation(x, y)
        last = (x, y)
    }
}
#endif
