import Foundation

/// One native WebView script execution path. This describes mechanics and does
/// not authorize source or select a page.
public struct ScriptExecutionCapability: Equatable, Sendable, Codable {
    public let supported: Bool
    public let frames: [String]
    public let runAt: [String]
    public let worlds: [String]
    public let rejectsStaleNavigation: Bool

    public init(supported: Bool, frames: [String] = [], runAt: [String] = [], worlds: [String] = [],
                rejectsStaleNavigation: Bool = false) {
        self.supported = supported; self.frames = frames; self.runAt = runAt; self.worlds = worlds
        self.rejectsStaleNavigation = rejectsStaleNavigation
    }
}

/// Current-page evaluation and future-document registration are separate
/// because native WebViews often support different timing and frame scopes.
public struct WebViewCapabilities: Equatable, Sendable, Codable {
    public let currentPage: ScriptExecutionCapability
    public let futureDocuments: ScriptExecutionCapability

    public init(currentPage: ScriptExecutionCapability, futureDocuments: ScriptExecutionCapability) {
        self.currentPage = currentPage; self.futureDocuments = futureDocuments
    }
}
