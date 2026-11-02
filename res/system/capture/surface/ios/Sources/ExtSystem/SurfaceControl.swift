import Foundation

#if os(macOS)
import AppKit
import ApplicationServices
import CoreGraphics
import ScreenCaptureKit

public enum SurfaceControlError: Error, Equatable {
    case invalidLimit
    case invalidInput
    case permissionDenied
    case staleReference
    case notFocused
    case captureTooLarge
    case nativeFailure
}

public struct SurfaceCaptureOptions: Equatable, Sendable {
    public let maxPixels: Int
    public let maxBytes: Int

    public init(maxPixels: Int = 16_777_216, maxBytes: Int = 16 * 1_024 * 1_024) throws {
        guard (1...268_435_456).contains(maxPixels), (1...(1 << 30)).contains(maxBytes) else {
            throw SurfaceControlError.invalidLimit
        }
        self.maxPixels = maxPixels
        self.maxBytes = maxBytes
    }
}

public struct SurfaceCapture: Codable, Equatable, Sendable {
    public let format: String
    public let width: Int
    public let height: Int
    public let data: Data

    public init(format: String, width: Int, height: Int, data: Data) {
        self.format = format
        self.width = width
        self.height = height
        self.data = data
    }
}

public struct SurfaceControlOptions: Equatable, Sendable {
    public let maxTextLength: Int

    public init(maxTextLength: Int = 4_096) throws {
        guard (1...1_000_000).contains(maxTextLength) else {
            throw SurfaceControlError.invalidLimit
        }
        self.maxTextLength = maxTextLength
    }
}

public enum SurfacePointerButton: String, Codable, Sendable {
    case left, right, middle
}

public struct SurfacePoint: Codable, Equatable, Sendable {
    public let x: Int
    public let y: Int

    public init(x: Int, y: Int) {
        self.x = x
        self.y = y
    }
}

/// Local capture and atomic pointer/keyboard primitives for observed macOS
/// windows. It owns no allowlist, approval UI, remote protocol, or long-lived
/// input session. Every call revalidates the opaque surface and process start.
@MainActor
public final class SystemSurfaceController {
    private struct NativeWindow {
        let info: WindowInfo
        let windowID: CGWindowID
    }

    private let options: SurfaceControlOptions

    public init(options: SurfaceControlOptions = try! SurfaceControlOptions()) {
        self.options = options
    }

    @available(macOS 14.0, *)
    public func capture(
        window: WindowInfo,
        options: SurfaceCaptureOptions = try! SurfaceCaptureOptions()
    ) async throws -> SurfaceCapture {
        guard CGPreflightScreenCaptureAccess() else { throw SurfaceControlError.permissionDenied }
        let native = try requireWindow(window)
        let content: SCShareableContent
        do {
            content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
        } catch {
            throw SurfaceControlError.nativeFailure
        }
        guard let captureWindow = content.windows.first(where: { $0.windowID == native.windowID }) else {
            throw SurfaceControlError.staleReference
        }
        let filter = SCContentFilter(desktopIndependentWindow: captureWindow)
        let scale = Double(filter.pointPixelScale)
        let width = max(1, Int(ceil(filter.contentRect.width * scale)))
        let height = max(1, Int(ceil(filter.contentRect.height * scale)))
        guard width <= options.maxPixels / height else { throw SurfaceControlError.captureTooLarge }
        let configuration = SCStreamConfiguration()
        configuration.width = width
        configuration.height = height
        configuration.captureResolution = .best
        let image: CGImage
        do {
            image = try await SCScreenshotManager.captureImage(
                contentFilter: filter,
                configuration: configuration
            )
        } catch {
            throw SurfaceControlError.nativeFailure
        }
        _ = try requireWindow(window)
        let bitmap = NSBitmapImageRep(cgImage: image)
        guard let data = bitmap.representation(using: .png, properties: [:]) else {
            throw SurfaceControlError.nativeFailure
        }
        guard data.count <= options.maxBytes else { throw SurfaceControlError.captureTooLarge }
        return SurfaceCapture(format: "image/png", width: image.width, height: image.height, data: data)
    }

    public func move(window: WindowInfo, x: Int, y: Int) throws {
        let native = try requireFocusedWindow(window)
        let location = try point(native.info, x: x, y: y)
        guard let event = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                                  mouseCursorPosition: location, mouseButton: .left) else {
            throw SurfaceControlError.nativeFailure
        }
        event.postToPid(pid_t(native.info.pid))
    }

    public func click(window: WindowInfo, x: Int, y: Int, button: SurfacePointerButton = .left) throws {
        let native = try requireFocusedWindow(window)
        let location = try point(native.info, x: x, y: y)
        let resolved: (CGMouseButton, CGEventType, CGEventType)
        switch button {
        case .left: resolved = (.left, .leftMouseDown, .leftMouseUp)
        case .right: resolved = (.right, .rightMouseDown, .rightMouseUp)
        case .middle: resolved = (.center, .otherMouseDown, .otherMouseUp)
        }
        guard let down = CGEvent(mouseEventSource: nil, mouseType: resolved.1,
                                 mouseCursorPosition: location, mouseButton: resolved.0),
              let up = CGEvent(mouseEventSource: nil, mouseType: resolved.2,
                               mouseCursorPosition: location, mouseButton: resolved.0) else {
            throw SurfaceControlError.nativeFailure
        }
        down.postToPid(pid_t(native.info.pid))
        up.postToPid(pid_t(native.info.pid))
    }

    public func drag(window: WindowInfo, from: SurfacePoint, to: SurfacePoint) throws {
        let native = try requireFocusedWindow(window)
        let start = try point(native.info, x: from.x, y: from.y)
        let end = try point(native.info, x: to.x, y: to.y)
        guard let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown,
                                 mouseCursorPosition: start, mouseButton: .left),
              let dragged = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDragged,
                                    mouseCursorPosition: end, mouseButton: .left),
              let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp,
                               mouseCursorPosition: end, mouseButton: .left) else {
            throw SurfaceControlError.nativeFailure
        }
        down.postToPid(pid_t(native.info.pid))
        dragged.postToPid(pid_t(native.info.pid))
        up.postToPid(pid_t(native.info.pid))
    }

    public func scroll(window: WindowInfo, x: Int, y: Int, deltaX: Int = 0, deltaY: Int) throws {
        let native = try requireFocusedWindow(window)
        let location = try point(native.info, x: x, y: y)
        guard deltaX >= Int(Int32.min), deltaX <= Int(Int32.max),
              deltaY >= Int(Int32.min), deltaY <= Int(Int32.max),
              let event = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 2,
                                  wheel1: Int32(deltaY), wheel2: Int32(deltaX), wheel3: 0) else {
            throw SurfaceControlError.invalidInput
        }
        event.location = location
        event.postToPid(pid_t(native.info.pid))
    }

    public func type(window: WindowInfo, text: String) throws {
        let native = try requireFocusedWindow(window)
        guard !text.isEmpty, text.count <= options.maxTextLength else {
            throw SurfaceControlError.invalidInput
        }
        guard let down = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: true),
              let up = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: false) else {
            throw SurfaceControlError.nativeFailure
        }
        let characters = Array(text.utf16)
        down.keyboardSetUnicodeString(stringLength: characters.count, unicodeString: characters)
        up.keyboardSetUnicodeString(stringLength: characters.count, unicodeString: characters)
        down.postToPid(pid_t(native.info.pid))
        up.postToPid(pid_t(native.info.pid))
    }

    public func press(window: WindowInfo, key: String, modifiers: Set<String> = []) throws {
        let native = try requireFocusedWindow(window)
        let keys: [String: CGKeyCode] = [
            "enter": 36, "tab": 48, "escape": 53, "space": 49,
            "left": 123, "right": 124, "down": 125, "up": 126,
            "backspace": 51, "delete": 117,
        ]
        guard let code = keys[key.lowercased()] else { throw SurfaceControlError.invalidInput }
        var flags: CGEventFlags = []
        for modifier in modifiers {
            switch modifier.lowercased() {
            case "command": flags.insert(.maskCommand)
            case "control": flags.insert(.maskControl)
            case "alt": flags.insert(.maskAlternate)
            case "shift": flags.insert(.maskShift)
            default: throw SurfaceControlError.invalidInput
            }
        }
        guard let down = CGEvent(keyboardEventSource: nil, virtualKey: code, keyDown: true),
              let up = CGEvent(keyboardEventSource: nil, virtualKey: code, keyDown: false) else {
            throw SurfaceControlError.nativeFailure
        }
        down.flags = flags
        up.flags = flags
        down.postToPid(pid_t(native.info.pid))
        up.postToPid(pid_t(native.info.pid))
    }

    private func requireWindow(_ target: WindowInfo) throws -> NativeWindow {
        guard target.id.hasPrefix("window:"), target.pid > 0, !target.processStartID.isEmpty else {
            throw SurfaceControlError.invalidInput
        }
        guard let values = CGWindowListCopyWindowInfo(
            [.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID
        ) as? [[String: Any]] else {
            throw SurfaceControlError.nativeFailure
        }
        for value in values {
            guard let pidValue = value[kCGWindowOwnerPID as String] as? NSNumber,
                  let windowValue = value[kCGWindowNumber as String] as? NSNumber,
                  let boundsValue = value[kCGWindowBounds as String] as? [String: Any],
                  let rectangle = CGRect(dictionaryRepresentation: boundsValue as CFDictionary),
                  rectangle.width > 0, rectangle.height > 0 else { continue }
            let pid = pidValue.intValue
            guard let application = NSRunningApplication(processIdentifier: pid_t(pid)),
                  let launched = application.launchDate else { continue }
            let startID = "\(pid):\(String(format: "%.6f", launched.timeIntervalSince1970))"
            let id = opaqueSurfaceID("window", String(pid), startID, String(windowValue.uint32Value))
            guard id == target.id else { continue }
            guard pid == target.pid, startID == target.processStartID else {
                throw SurfaceControlError.staleReference
            }
            return NativeWindow(
                info: WindowInfo(id: id, pid: pid, processStartID: startID,
                                 bounds: surfaceBounds(rectangle), onScreen: true),
                windowID: windowValue.uint32Value
            )
        }
        throw SurfaceControlError.staleReference
    }

    private func requireFocusedWindow(_ target: WindowInfo) throws -> NativeWindow {
        guard AXIsProcessTrusted() else { throw SurfaceControlError.permissionDenied }
        let native = try requireWindow(target)
        let root = AXUIElementCreateApplication(pid_t(native.info.pid))
        AXUIElementSetMessagingTimeout(root, 0.25)
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(root, kAXFocusedWindowAttribute as CFString, &value) == .success,
              let value, CFGetTypeID(value) == AXUIElementGetTypeID() else {
            throw SurfaceControlError.notFocused
        }
        let focused = unsafeBitCast(value, to: AXUIElement.self)
        var positionValue: CFTypeRef?
        var sizeValue: CFTypeRef?
        guard AXUIElementCopyAttributeValue(focused, kAXPositionAttribute as CFString, &positionValue) == .success,
              AXUIElementCopyAttributeValue(focused, kAXSizeAttribute as CFString, &sizeValue) == .success,
              let positionValue, let sizeValue,
              CFGetTypeID(positionValue) == AXValueGetTypeID(),
              CFGetTypeID(sizeValue) == AXValueGetTypeID() else {
            throw SurfaceControlError.notFocused
        }
        var position = CGPoint.zero
        var size = CGSize.zero
        guard AXValueGetValue(unsafeBitCast(positionValue, to: AXValue.self), .cgPoint, &position),
              AXValueGetValue(unsafeBitCast(sizeValue, to: AXValue.self), .cgSize, &size),
              abs(position.x - Double(native.info.bounds.x)) < 1,
              abs(position.y - Double(native.info.bounds.y)) < 1,
              abs(size.width - Double(native.info.bounds.width)) < 1,
              abs(size.height - Double(native.info.bounds.height)) < 1 else {
            throw SurfaceControlError.notFocused
        }
        return native
    }

    private func point(_ window: WindowInfo, x: Int, y: Int) throws -> CGPoint {
        guard x >= 0, y >= 0, x < window.bounds.width, y < window.bounds.height else {
            throw SurfaceControlError.invalidInput
        }
        return CGPoint(x: window.bounds.x + x, y: window.bounds.y + y)
    }
}
#endif
