import CryptoKit
import Foundation

#if os(macOS)
import AppKit
import CoreGraphics
#elseif os(iOS)
import UIKit
#endif

public struct SurfaceBounds: Codable, Equatable, Sendable {
    public let x: Int
    public let y: Int
    public let width: Int
    public let height: Int

    public init(x: Int, y: Int, width: Int, height: Int) {
        self.x = x
        self.y = y
        self.width = width
        self.height = height
    }
}

public struct DisplayInfo: Codable, Equatable, Sendable {
    public let id: String
    public let bounds: SurfaceBounds
    public let scale: Double
    public let primary: Bool

    public init(id: String, bounds: SurfaceBounds, scale: Double = 0, primary: Bool = false) {
        self.id = id
        self.bounds = bounds
        self.scale = scale
        self.primary = primary
    }
}

public struct WindowInfo: Codable, Equatable, Sendable {
    public let id: String
    public let pid: Int
    public let processStartID: String
    public let bounds: SurfaceBounds
    public let onScreen: Bool

    public init(id: String, pid: Int, processStartID: String, bounds: SurfaceBounds, onScreen: Bool) {
        self.id = id
        self.pid = pid
        self.processStartID = processStartID
        self.bounds = bounds
        self.onScreen = onScreen
    }

    enum CodingKeys: String, CodingKey {
        case id, pid, bounds
        case processStartID = "process_start_id"
        case onScreen = "on_screen"
    }
}

public struct SurfaceCollectionStatus: Codable, Equatable, Sendable {
    public let state: String
    public let detail: String?
    public let observedAt: String?

    public init(state: String, detail: String? = nil, observedAt: String? = nil) {
        self.state = state
        self.detail = detail
        self.observedAt = observedAt
    }

    enum CodingKeys: String, CodingKey {
        case state, detail
        case observedAt = "observed_at"
    }
}

public struct SurfaceSnapshot: Codable, Equatable, Sendable {
    public let platform: String
    public let coordinateSpace: String
    public let observedAt: String
    public let displays: [DisplayInfo]
    public let windows: [WindowInfo]
    public let displayStatus: SurfaceCollectionStatus
    public let windowStatus: SurfaceCollectionStatus

    public init(platform: String, coordinateSpace: String, observedAt: String,
                displays: [DisplayInfo], windows: [WindowInfo],
                displayStatus: SurfaceCollectionStatus, windowStatus: SurfaceCollectionStatus) {
        self.platform = platform
        self.coordinateSpace = coordinateSpace
        self.observedAt = observedAt
        self.displays = displays
        self.windows = windows
        self.displayStatus = displayStatus
        self.windowStatus = windowStatus
    }

    enum CodingKeys: String, CodingKey {
        case platform, displays, windows
        case coordinateSpace = "coordinate_space"
        case observedAt = "observed_at"
        case displayStatus = "display_status"
        case windowStatus = "window_status"
    }
}

public struct SurfaceOptions: Equatable, Sendable {
    public let maxDisplays: Int
    public let maxWindows: Int

    public init(maxDisplays: Int = 64, maxWindows: Int = 4096) throws {
        guard (1...65_536).contains(maxDisplays), (1...65_536).contains(maxWindows) else {
            throw SurfaceObservationError.invalidLimit
        }
        self.maxDisplays = maxDisplays
        self.maxWindows = maxWindows
    }
}

public enum SurfaceObservationError: Error, Equatable {
    case invalidLimit
    case nativeFailure
}

/// A caller-owned observer for local display and window metadata. IDs are
/// opaque. The macOS surface controller accepts the complete WindowInfo value
/// and revalidates its ID and process generation before acting on it.
@MainActor
public final class SystemSurfaceObserver {
    private let options: SurfaceOptions

    public init(options: SurfaceOptions = try! SurfaceOptions()) {
        self.options = options
    }

    public func observe() throws -> SurfaceSnapshot {
#if os(macOS)
        return try observeMacOS()
#elseif os(iOS)
        return observeIOS()
#else
        throw SurfaceObservationError.nativeFailure
#endif
    }
}

func opaqueSurfaceID(_ kind: String, _ components: String...) -> String {
    let source = ([kind] + components).joined(separator: "\0")
    let digest = SHA256.hash(data: Data(source.utf8)).prefix(12)
    return "\(kind):" + digest.map { String(format: "%02x", $0) }.joined()
}

func surfaceBounds(_ rectangle: CGRect) -> SurfaceBounds {
    SurfaceBounds(x: Int(rectangle.origin.x), y: Int(rectangle.origin.y),
                  width: Int(rectangle.width), height: Int(rectangle.height))
}

func surfaceTimestamp(_ date: Date) -> String {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter.string(from: date)
}

#if os(macOS)
extension SystemSurfaceObserver {
    private func observeMacOS() throws -> SurfaceSnapshot {
        let now = surfaceTimestamp(Date())
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(0, nil, &count) == .success else {
            throw SurfaceObservationError.nativeFailure
        }
        var identifiers = [CGDirectDisplayID](repeating: 0, count: Int(count))
        guard CGGetActiveDisplayList(count, &identifiers, &count) == .success else {
            throw SurfaceObservationError.nativeFailure
        }
        let displayLimited = identifiers.count > options.maxDisplays
        let displays = identifiers.prefix(options.maxDisplays).compactMap { identifier -> DisplayInfo? in
            let rectangle = CGDisplayBounds(identifier)
            guard rectangle.width > 0, rectangle.height > 0 else { return nil }
            return DisplayInfo(
                id: opaqueSurfaceID("display", String(identifier)),
                bounds: surfaceBounds(rectangle),
                scale: Double(CGDisplayPixelsWide(identifier)) / rectangle.width,
                primary: identifier == CGMainDisplayID()
            )
        }.sorted { $0.id < $1.id }

        guard let values = CGWindowListCopyWindowInfo(
            [.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID
        ) as? [[String: Any]] else {
            return SurfaceSnapshot(
                platform: "darwin", coordinateSpace: "desktop-global-points", observedAt: now,
                displays: displays, windows: [],
                displayStatus: SurfaceCollectionStatus(state: displayLimited ? "partial" : "complete", detail: displayLimited ? "display limit reached" : nil, observedAt: now),
                windowStatus: SurfaceCollectionStatus(state: "unavailable", detail: "Core Graphics window enumeration failed", observedAt: now)
            )
        }
        var windows: [WindowInfo] = []
        var sawExtraWindow = false
        var identityUnavailable = false
        for value in values {
            guard let pidValue = value[kCGWindowOwnerPID as String] as? NSNumber,
                  let windowValue = value[kCGWindowNumber as String] as? NSNumber,
                  let boundsValue = value[kCGWindowBounds as String] as? [String: Any],
                  let rectangle = CGRect(dictionaryRepresentation: boundsValue as CFDictionary),
                  rectangle.width > 0, rectangle.height > 0 else { continue }
            if windows.count >= options.maxWindows {
                sawExtraWindow = true
                continue
            }
            let pid = pidValue.intValue
            guard let application = NSRunningApplication(processIdentifier: pid_t(pid)),
                  let launched = application.launchDate else {
                identityUnavailable = true
                continue
            }
            let startID = "\(pid):\(String(format: "%.6f", launched.timeIntervalSince1970))"
            windows.append(WindowInfo(
                id: opaqueSurfaceID("window", String(pid), startID, String(windowValue.uint32Value)),
                pid: pid, processStartID: startID, bounds: surfaceBounds(rectangle), onScreen: true
            ))
        }
        windows.sort { $0.id < $1.id }
        return SurfaceSnapshot(
            platform: "darwin", coordinateSpace: "desktop-global-points", observedAt: now,
            displays: displays, windows: windows,
            displayStatus: SurfaceCollectionStatus(state: displayLimited ? "partial" : "complete", detail: displayLimited ? "display limit reached" : nil, observedAt: now),
            windowStatus: SurfaceCollectionStatus(
                state: (sawExtraWindow || identityUnavailable) ? "partial" : "complete",
                detail: sawExtraWindow ? "window limit reached" : (identityUnavailable ? "some window process generations were unavailable" : nil),
                observedAt: now
            )
        )
    }
}
#elseif os(iOS)
let iosProcessGeneration = UUID().uuidString.lowercased()

extension SystemSurfaceObserver {
    private func observeIOS() -> SurfaceSnapshot {
        let now = surfaceTimestamp(Date())
        var screens: [UIScreen] = []
        for scene in UIApplication.shared.connectedScenes {
            guard let windowScene = scene as? UIWindowScene,
                  !screens.contains(where: { $0 === windowScene.screen }) else { continue }
            screens.append(windowScene.screen)
        }
        if screens.isEmpty { screens = [UIScreen.main] }
        let displayLimited = screens.count > options.maxDisplays
        let displays = screens.prefix(options.maxDisplays).enumerated().map { index, screen in
            DisplayInfo(id: opaqueSurfaceID("display", String(index), String(describing: ObjectIdentifier(screen))),
                        bounds: surfaceBounds(screen.bounds), scale: Double(screen.scale), primary: index == 0)
        }.sorted { $0.id < $1.id }

        let pid = Int(ProcessInfo.processInfo.processIdentifier)
        var windows: [WindowInfo] = []
        var sawExtraWindow = false
        for scene in UIApplication.shared.connectedScenes {
            guard let windowScene = scene as? UIWindowScene else { continue }
            for window in windowScene.windows where !window.isHidden && window.alpha > 0 {
                if windows.count >= options.maxWindows {
                    sawExtraWindow = true
                    continue
                }
                let rectangle = window.frame
                guard rectangle.width > 0, rectangle.height > 0 else { continue }
                windows.append(WindowInfo(
                    id: opaqueSurfaceID("window", String(pid), iosProcessGeneration, String(describing: ObjectIdentifier(window))),
                    pid: pid, processStartID: iosProcessGeneration, bounds: surfaceBounds(rectangle), onScreen: true
                ))
            }
        }
        windows.sort { $0.id < $1.id }
        return SurfaceSnapshot(
            platform: "ios", coordinateSpace: "screen-points", observedAt: now,
            displays: displays, windows: windows,
            displayStatus: SurfaceCollectionStatus(state: displayLimited ? "partial" : "complete", detail: displayLimited ? "display limit reached" : nil, observedAt: now),
            windowStatus: SurfaceCollectionStatus(state: sawExtraWindow ? "partial" : "complete", detail: sawExtraWindow ? "window limit reached" : nil, observedAt: now)
        )
    }
}
#endif
