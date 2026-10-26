import Foundation

#if os(macOS)
import ApplicationServices
import CoreGraphics
#elseif os(iOS)
import ReplayKit
#endif

public struct PermissionInfo: Codable, Equatable, Sendable {
    public let state: String
    public let scope: String?
    public let detail: String?

    public init(state: String, scope: String? = nil, detail: String? = nil) {
        self.state = state; self.scope = scope; self.detail = detail
    }
}

public struct PermissionSnapshot: Codable, Equatable, Sendable {
    public let platform: String
    public let observedAt: String
    public let accessibility: PermissionInfo
    public let screenRecording: PermissionInfo

    public init(platform: String, observedAt: String, accessibility: PermissionInfo,
                screenRecording: PermissionInfo) {
        self.platform = platform; self.observedAt = observedAt
        self.accessibility = accessibility; self.screenRecording = screenRecording
    }

    enum CodingKeys: String, CodingKey {
        case platform, accessibility
        case observedAt = "observed_at"
        case screenRecording = "screen_recording"
    }
}

/// Reads native permission state without displaying a prompt or opening OS
/// settings.
@MainActor
public enum SystemPermissionObserver {
    public static func observe() -> PermissionSnapshot {
        let now = surfaceTimestamp(Date())
#if os(macOS)
        return PermissionSnapshot(
            platform: "darwin", observedAt: now,
            accessibility: AXIsProcessTrusted()
                ? PermissionInfo(state: "ready", scope: "macos.accessibility")
                : PermissionInfo(state: "denied", scope: "macos.accessibility", detail: "Accessibility access is not granted"),
            screenRecording: CGPreflightScreenCaptureAccess()
                ? PermissionInfo(state: "ready", scope: "macos.screen-recording")
                : PermissionInfo(state: "denied", scope: "macos.screen-recording", detail: "Screen Recording access is not granted")
        )
#elseif os(iOS)
        let recorder = RPScreenRecorder.shared()
        return PermissionSnapshot(
            platform: "ios", observedAt: now,
            accessibility: PermissionInfo(state: "not-required", scope: "ios.app-owned-ui"),
            screenRecording: recorder.isAvailable
                ? PermissionInfo(state: "not-determined", scope: "ios.replaykit", detail: "ReplayKit consent is determined when recording starts")
                : PermissionInfo(state: "unavailable", scope: "ios.replaykit", detail: "ReplayKit recording is unavailable")
        )
#endif
    }
}
