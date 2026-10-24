#if canImport(WebKit)
import Foundation

// Objective-C access to the web-view host. Calls complete on the main queue.

public let EXTWebViewPluginHostErrorDomain = "EXTWebViewPluginHostErrorDomain"

/// Error codes in EXTWebViewPluginHostErrorDomain. A guest status is 100 plus the
/// ext_plugin_call status (101 invalid, 102 closed, 103 failed).
@objc(EXTWebViewPluginHostError)
public enum ObjCWebViewPluginHostError: Int {
    case notStarted = 1
    case closed = 2
    case timeout = 3
    case page = 4
}

@MainActor
@objc(EXTWebViewPluginHost)
public final class ObjCWebViewPluginHost: NSObject {
    private let host: WebViewPluginHost

    /// - Parameter bridge: the page shim (res/web/bundle/shim.js); nil uses the embedded copy.
    @objc public init(module: Data, bridge: String?, log: ((String) -> Void)?) {
        host = WebViewPluginHost(module: module, bridge: bridge, log: log)
    }

    @objc public func start(timeout: TimeInterval, completion: @escaping (NSError?) -> Void) {
        Task { @MainActor in
            do {
                try await host.start(timeout: timeout)
                completion(nil)
            } catch {
                completion(Self.nsError(error))
            }
        }
    }

    @objc public func handshake(timeout: TimeInterval, completion: @escaping (Data?, NSError?) -> Void) {
        Task { @MainActor in
            do {
                completion(try await host.handshake(deadline: Date().addingTimeInterval(timeout)), nil)
            } catch {
                completion(nil, Self.nsError(error))
            }
        }
    }

    @objc public func invoke(_ request: Data, timeout: TimeInterval, completion: @escaping (Data?, NSError?) -> Void) {
        Task { @MainActor in
            do {
                completion(try await host.invoke(request, timeout: timeout), nil)
            } catch {
                completion(nil, Self.nsError(error))
            }
        }
    }

    @objc public func close() { host.close() }

    private static func nsError(_ error: Error) -> NSError {
        guard let failure = error as? WebViewPluginHost.Failure else {
            return NSError(domain: EXTWebViewPluginHostErrorDomain, code: ObjCWebViewPluginHostError.page.rawValue,
                           userInfo: [NSLocalizedDescriptionKey: String(describing: error)])
        }
        switch failure {
        case .notStarted: return NSError(domain: EXTWebViewPluginHostErrorDomain, code: 1)
        case .closed: return NSError(domain: EXTWebViewPluginHostErrorDomain, code: 2)
        case .timeout: return NSError(domain: EXTWebViewPluginHostErrorDomain, code: 3)
        case .page(let reason):
            return NSError(domain: EXTWebViewPluginHostErrorDomain, code: 4, userInfo: [NSLocalizedDescriptionKey: reason])
        case .status(let status):
            return NSError(domain: EXTWebViewPluginHostErrorDomain, code: 100 + status)
        }
    }
}
#endif
