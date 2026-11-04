import CryptoKit
import Foundation

#if os(macOS)
import AppKit
import ApplicationServices
#elseif os(iOS)
import UIKit
#endif

public struct AccessibilityElement: Codable, Equatable, Sendable {
    public let id: String
    public let parentID: String?
    public let pid: Int
    public let processStartID: String
    public let role: String
    public let identifier: String?
    public let label: String?
    public let value: String?
    public let bounds: SurfaceBounds?
    public let actions: [String]
    public let enabled: Bool?
    public let focused: Bool?

    public init(id: String, parentID: String? = nil, pid: Int, processStartID: String,
                role: String, identifier: String? = nil, label: String? = nil,
                value: String? = nil, bounds: SurfaceBounds? = nil, actions: [String] = [],
                enabled: Bool? = nil, focused: Bool? = nil) {
        self.id = id; self.parentID = parentID; self.pid = pid; self.processStartID = processStartID
        self.role = role; self.identifier = identifier; self.label = label; self.value = value
        self.bounds = bounds; self.actions = actions; self.enabled = enabled; self.focused = focused
    }

    enum CodingKeys: String, CodingKey {
        case id, pid, role, identifier, label, value, bounds, actions, enabled, focused
        case parentID = "parent_id"
        case processStartID = "process_start_id"
    }
}

public struct AccessibilitySnapshot: Codable, Equatable, Sendable {
    public let platform: String
    public let observedAt: String
    public let status: SurfaceCollectionStatus
    public let elements: [AccessibilityElement]

    public init(platform: String, observedAt: String, status: SurfaceCollectionStatus,
                elements: [AccessibilityElement]) {
        self.platform = platform; self.observedAt = observedAt; self.status = status; self.elements = elements
    }

    enum CodingKeys: String, CodingKey {
        case platform, status, elements
        case observedAt = "observed_at"
    }
}

public struct AccessibilityOptions: Equatable, Sendable {
    public let maxElements: Int
    public let maxDepth: Int
    public let includeText: Bool

    public init(maxElements: Int = 1024, maxDepth: Int = 16, includeText: Bool = false) throws {
        guard (1...65_536).contains(maxElements), (1...256).contains(maxDepth) else {
            throw AccessibilityObservationError.invalidLimit
        }
        self.maxElements = maxElements; self.maxDepth = maxDepth; self.includeText = includeText
    }
}

public enum AccessibilityObservationError: Error, Equatable {
    case invalidLimit
}

/// Read-only observation of the focused native UI tree on macOS and the
/// application's own UIKit tree on iOS. It never requests permission or
/// exposes an invocable native reference.
@MainActor
public final class SystemAccessibilityObserver {
    private let options: AccessibilityOptions

    public init(options: AccessibilityOptions = try! AccessibilityOptions()) {
        self.options = options
    }

    public func observe() -> AccessibilitySnapshot {
#if os(macOS)
        observeMacOS()
#elseif os(iOS)
        observeIOS()
#endif
    }
}

func accessibilityID(_ pid: Int, _ generation: String, _ path: String) -> String {
    let digest = SHA256.hash(data: Data("accessibility\0\(pid)\0\(generation)\0\(path)".utf8)).prefix(12)
    return "accessibility:" + digest.map { String(format: "%02x", $0) }.joined()
}

#if os(macOS)
extension SystemAccessibilityObserver {
    private struct Node {
        let element: AXUIElement
        let parentID: String?
        let path: String
        let depth: Int
    }

    private func observeMacOS() -> AccessibilitySnapshot {
        let now = surfaceTimestamp(Date())
        guard AXIsProcessTrusted() else {
            return AccessibilitySnapshot(platform: "darwin", observedAt: now,
                status: SurfaceCollectionStatus(state: "permission-denied", detail: "macOS Accessibility access is not granted", observedAt: now), elements: [])
        }
        let system = AXUIElementCreateSystemWide()
        var focusedValue: CFTypeRef?
        guard AXUIElementCopyAttributeValue(system, kAXFocusedApplicationAttribute as CFString, &focusedValue) == .success,
              let focusedValue, CFGetTypeID(focusedValue) == AXUIElementGetTypeID() else {
            return AccessibilitySnapshot(platform: "darwin", observedAt: now,
                status: SurfaceCollectionStatus(state: "unavailable", detail: "focused application is unavailable", observedAt: now), elements: [])
        }
        let root = unsafeBitCast(focusedValue, to: AXUIElement.self)
        var nativePID: pid_t = 0
        guard AXUIElementGetPid(root, &nativePID) == .success,
              let application = NSRunningApplication(processIdentifier: nativePID),
              let launchDate = application.launchDate else {
            return AccessibilitySnapshot(platform: "darwin", observedAt: now,
                status: SurfaceCollectionStatus(state: "unavailable", detail: "focused application generation is unavailable", observedAt: now), elements: [])
        }
        let pid = Int(nativePID)
        let generation = "\(pid):\(String(format: "%.6f", launchDate.timeIntervalSince1970))"
        var queue = [Node(element: root, parentID: nil, path: "0", depth: 0)]
        var elements: [AccessibilityElement] = []
        var truncated = false
        while !queue.isEmpty {
            let node = queue.removeFirst()
            guard elements.count < options.maxElements else { truncated = true; continue }
            let id = accessibilityID(pid, generation, node.path)
            let role = stringAttribute(node.element, kAXRoleAttribute) ?? "unknown"
            let identifier = stringAttribute(node.element, kAXIdentifierAttribute)
            let label = options.includeText ? stringAttribute(node.element, kAXTitleAttribute) : nil
            let value = options.includeText ? stringAttribute(node.element, kAXValueAttribute) : nil
            let actions = actionNames(node.element).sorted()
            elements.append(AccessibilityElement(
                id: id, parentID: node.parentID, pid: pid, processStartID: generation,
                role: role, identifier: identifier, label: label, value: value,
                bounds: elementBounds(node.element), actions: actions,
                enabled: boolAttribute(node.element, kAXEnabledAttribute),
                focused: boolAttribute(node.element, kAXFocusedAttribute)
            ))
            let children = elementChildren(node.element)
            if node.depth >= options.maxDepth {
                truncated = truncated || !children.isEmpty
                continue
            }
            for (index, child) in children.enumerated() {
                queue.append(Node(element: child, parentID: id, path: "\(node.path)/\(index)", depth: node.depth + 1))
            }
        }
        elements.sort { $0.id < $1.id }
        return AccessibilitySnapshot(platform: "darwin", observedAt: now,
            status: SurfaceCollectionStatus(state: truncated ? "partial" : "complete",
                detail: truncated ? "accessibility element or depth limit reached" : nil, observedAt: now), elements: elements)
    }
}

func stringAttribute(_ element: AXUIElement, _ attribute: String) -> String? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute as CFString, &value) == .success else { return nil }
    return value as? String
}

func boolAttribute(_ element: AXUIElement, _ attribute: String) -> Bool? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute as CFString, &value) == .success else { return nil }
    return value as? Bool
}

func elementChildren(_ element: AXUIElement) -> [AXUIElement] {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &value) == .success,
          let children = value as? [AXUIElement] else { return [] }
    return children
}

func actionNames(_ element: AXUIElement) -> [String] {
    var values: CFArray?
    guard AXUIElementCopyActionNames(element, &values) == .success else { return [] }
    return values as? [String] ?? []
}

func elementBounds(_ element: AXUIElement) -> SurfaceBounds? {
    var positionValue: CFTypeRef?
    var sizeValue: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, kAXPositionAttribute as CFString, &positionValue) == .success,
          AXUIElementCopyAttributeValue(element, kAXSizeAttribute as CFString, &sizeValue) == .success,
          let position = positionValue, let size = sizeValue,
          CFGetTypeID(position) == AXValueGetTypeID(), CFGetTypeID(size) == AXValueGetTypeID() else { return nil }
    var point = CGPoint.zero
    var dimensions = CGSize.zero
    guard AXValueGetValue(unsafeBitCast(position, to: AXValue.self), .cgPoint, &point),
          AXValueGetValue(unsafeBitCast(size, to: AXValue.self), .cgSize, &dimensions),
          dimensions.width > 0, dimensions.height > 0 else { return nil }
    return surfaceBounds(CGRect(origin: point, size: dimensions))
}
#elseif os(iOS)
extension SystemAccessibilityObserver {
    private struct Node {
        let view: UIView
        let parentID: String?
        let path: String
        let depth: Int
    }

    private func observeIOS() -> AccessibilitySnapshot {
        let now = surfaceTimestamp(Date())
        let pid = Int(ProcessInfo.processInfo.processIdentifier)
        var roots: [UIView] = []
        for scene in UIApplication.shared.connectedScenes {
            guard let windowScene = scene as? UIWindowScene else { continue }
            roots.append(contentsOf: windowScene.windows.filter { !$0.isHidden && $0.alpha > 0 })
        }
        var queue = roots.enumerated().map { Node(view: $0.element, parentID: nil, path: String($0.offset), depth: 0) }
        var elements: [AccessibilityElement] = []
        var truncated = false
        while !queue.isEmpty {
            let node = queue.removeFirst()
            guard elements.count < options.maxElements else { truncated = true; continue }
            let id = accessibilityID(pid, iosProcessGeneration, node.path)
            let frame = node.view.convert(node.view.bounds, to: nil)
            let bounds = frame.width > 0 && frame.height > 0 ? surfaceBounds(frame) : nil
            let enabled = (node.view as? UIControl)?.isEnabled ?? node.view.isUserInteractionEnabled
            var actions: [String] = []
            if node.view.isUserInteractionEnabled { actions.append("activate") }
            if node.view.accessibilityTraits.contains(.adjustable) { actions.append(contentsOf: ["decrement", "increment"]) }
            elements.append(AccessibilityElement(
                id: id, parentID: node.parentID, pid: pid, processStartID: iosProcessGeneration,
                role: String(describing: type(of: node.view)), identifier: node.view.accessibilityIdentifier,
                label: options.includeText ? node.view.accessibilityLabel : nil,
                value: options.includeText ? node.view.accessibilityValue : nil,
                bounds: bounds, actions: actions.sorted(), enabled: enabled, focused: node.view.isFirstResponder
            ))
            if node.depth >= options.maxDepth {
                truncated = truncated || !node.view.subviews.isEmpty
                continue
            }
            for (index, child) in node.view.subviews.enumerated() {
                queue.append(Node(view: child, parentID: id, path: "\(node.path)/\(index)", depth: node.depth + 1))
            }
        }
        elements.sort { $0.id < $1.id }
        return AccessibilitySnapshot(platform: "ios", observedAt: now,
            status: SurfaceCollectionStatus(state: truncated ? "partial" : "complete",
                detail: truncated ? "accessibility element or depth limit reached" : nil, observedAt: now), elements: elements)
    }
}
#endif
