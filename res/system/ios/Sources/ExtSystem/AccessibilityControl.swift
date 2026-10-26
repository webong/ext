import Foundation

#if os(macOS)
import AppKit
import ApplicationServices

public enum AccessibilityControlError: Error, Equatable {
    case invalidInput
    case permissionDenied
    case staleReference
    case unsupported
    case nativeFailure
}

public enum AccessibilityAttributeValue: Equatable, Sendable {
    case string(String)
    case boolean(Bool)
    case number(Double)
    case point(x: Double, y: Double)
    case size(width: Double, height: Double)
    case range(location: Int, length: Int)
}

/// Performs one native Accessibility action or attribute update on an element
/// returned by SystemAccessibilityObserver. The controller re-walks the
/// bounded tree and verifies the process generation and element description;
/// it retains no native handles and owns no allowlist or approval policy.
@MainActor
public final class SystemAccessibilityController {
    private struct Node {
        let element: AXUIElement
        let path: String
        let depth: Int
    }

    private let options: AccessibilityOptions

    public init(options: AccessibilityOptions = try! AccessibilityOptions()) {
        self.options = options
    }

    public func perform(element target: AccessibilityElement, action: String) throws {
        guard action.hasPrefix("AX"), !action.contains("\n"), action.count <= 128 else {
            throw AccessibilityControlError.invalidInput
        }
        guard target.actions.contains(action) else {
            throw AccessibilityControlError.unsupported
        }
        let native = try resolve(target)
        guard actionNames(native).contains(action) else {
            throw AccessibilityControlError.staleReference
        }
        guard AXUIElementPerformAction(native, action as CFString) == .success else {
            throw AccessibilityControlError.nativeFailure
        }
    }

    public func set(
        element target: AccessibilityElement,
        attribute: String,
        value: AccessibilityAttributeValue
    ) throws {
        guard attribute.hasPrefix("AX"), !attribute.contains("\n"), attribute.count <= 128 else {
            throw AccessibilityControlError.invalidInput
        }
        let native = try resolve(target)
        var settable = DarwinBoolean(false)
        guard AXUIElementIsAttributeSettable(native, attribute as CFString, &settable) == .success,
              settable.boolValue else {
            throw AccessibilityControlError.unsupported
        }
        let encoded = try encode(value)
        guard AXUIElementSetAttributeValue(native, attribute as CFString, encoded) == .success else {
            throw AccessibilityControlError.nativeFailure
        }
    }

    private func resolve(_ target: AccessibilityElement) throws -> AXUIElement {
        guard AXIsProcessTrusted() else { throw AccessibilityControlError.permissionDenied }
        guard target.pid > 0, !target.processStartID.isEmpty,
              let application = NSRunningApplication(processIdentifier: pid_t(target.pid)),
              let launchDate = application.launchDate else {
            throw AccessibilityControlError.staleReference
        }
        let generation = "\(target.pid):\(String(format: "%.6f", launchDate.timeIntervalSince1970))"
        guard generation == target.processStartID else {
            throw AccessibilityControlError.staleReference
        }
        let root = AXUIElementCreateApplication(pid_t(target.pid))
        AXUIElementSetMessagingTimeout(root, 0.25)
        var queue = [Node(element: root, path: "0", depth: 0)]
        var cursor = 0
        while cursor < queue.count, cursor < options.maxElements {
            let node = queue[cursor]
            cursor += 1
            AXUIElementSetMessagingTimeout(node.element, 0.25)
            if accessibilityID(target.pid, generation, node.path) == target.id {
                guard matches(node.element, target) else {
                    throw AccessibilityControlError.staleReference
                }
                return node.element
            }
            guard node.depth < options.maxDepth else { continue }
            let remaining = options.maxElements - queue.count
            if remaining <= 0 { continue }
            queue.append(contentsOf: elementChildren(node.element).prefix(remaining).enumerated().map {
                Node(element: $0.element, path: "\(node.path)/\($0.offset)", depth: node.depth + 1)
            })
        }
        throw AccessibilityControlError.staleReference
    }

    private func matches(_ element: AXUIElement, _ target: AccessibilityElement) -> Bool {
        guard (stringAttribute(element, kAXRoleAttribute) ?? "unknown") == target.role,
              stringAttribute(element, kAXIdentifierAttribute) == target.identifier,
              elementBounds(element) == target.bounds else {
            return false
        }
        return true
    }

    private func encode(_ value: AccessibilityAttributeValue) throws -> CFTypeRef {
        switch value {
        case .string(let text):
            guard text.utf8.count <= 1_048_576 else { throw AccessibilityControlError.invalidInput }
            return text as CFString
        case .boolean(let boolean):
            return boolean as CFBoolean
        case .number(let number):
            guard number.isFinite else { throw AccessibilityControlError.invalidInput }
            return number as CFNumber
        case .point(let x, let y):
            guard x.isFinite, y.isFinite, abs(x) <= 1_000_000, abs(y) <= 1_000_000 else {
                throw AccessibilityControlError.invalidInput
            }
            var point = CGPoint(x: x, y: y)
            guard let encoded = AXValueCreate(.cgPoint, &point) else {
                throw AccessibilityControlError.invalidInput
            }
            return encoded
        case .size(let width, let height):
            guard width.isFinite, height.isFinite, width > 0, height > 0,
                  width <= 100_000, height <= 100_000 else {
                throw AccessibilityControlError.invalidInput
            }
            var size = CGSize(width: width, height: height)
            guard let encoded = AXValueCreate(.cgSize, &size) else {
                throw AccessibilityControlError.invalidInput
            }
            return encoded
        case .range(let location, let length):
            guard location >= 0, length >= 0, location <= 1_000_000 - length else {
                throw AccessibilityControlError.invalidInput
            }
            var range = CFRange(location: location, length: length)
            guard let encoded = AXValueCreate(.cfRange, &range) else {
                throw AccessibilityControlError.invalidInput
            }
            return encoded
        }
    }
}
#endif
