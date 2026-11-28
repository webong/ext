import Foundation

/// Network policy for a local web bundle. This is a resource policy, not a sandbox.
public struct BundlePolicy: Equatable {
    public var allowNet: Bool
    public var allowOrigins: [String]
    public init(allowNet: Bool = false, allowOrigins: [String] = []) {
        self.allowNet = allowNet; self.allowOrigins = allowOrigins
    }
    public var contentSecurityPolicy: String {
        var connect = "'self'", resources = "'self' data: blob:"
        if allowNet { connect = "*"; resources = "* data: blob:" }
        else if !allowOrigins.isEmpty {
            let extra = allowOrigins.filter {
                ($0.hasPrefix("https://") || $0.hasPrefix("http://")) &&
                $0.rangeOfCharacter(from: CharacterSet(charactersIn: " ;,'\"\r\n\t")) == nil &&
                $0.filter { $0 == "/" }.count == 2
            }.joined(separator: " ")
            connect += " " + extra; resources += " " + extra
        }
        return ["default-src 'none'", "script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval' blob:",
            "style-src 'self' 'unsafe-inline'", "connect-src " + connect,
            "img-src " + resources, "font-src " + resources, "media-src " + resources,
            "worker-src 'self' blob:", "form-action 'none'", "frame-src 'none'",
            "object-src 'none'", "base-uri 'self'"].joined(separator: "; ")
    }
}
