import Foundation
@preconcurrency import WebKit
import ExtWeb

/// One local bundle run. Create and attach `webView`, then await `run()` on the main actor.
/// The view uses an ephemeral store. Navigation stays on its loopback origin; resources
/// are governed by CSP. This runs trusted application content, not a capability sandbox.
@MainActor
public final class BundleRunner: NSObject, WKNavigationDelegate, WKUIDelegate {
    public struct Options {
        public var root: URL
        public var entry = "index.html"
        public var policy = BundlePolicy()
        public var crossOriginIsolation = false
        public var timeout: TimeInterval = 0
        public var startupTimeout: TimeInterval = 30
        public var heartbeatTimeout: TimeInterval = 15
        public init(root: URL) { self.root = root }
    }
    public struct Result: Equatable {
        public let status: String
        public let exitCode: Int
        public let reason: String
        public let url: URL?
    }
    public struct Event {
        public let level: String
        public let text: String
    }
    public enum Failure: Error { case invalidBundle, alreadyStarted, closed, queueFull, messageTooLarge }
    public let webView: WKWebView
    /// Callbacks run on the main actor. Set them before calling run().
    public var onConsole: ((Event) -> Void)?
    public var onMessage: ((String) -> Void)?
    public var onReady: (() -> Void)?
    private let server: BundleServer
    private var waiting: CheckedContinuation<Result, Never>?
    private var result: Result?
    private var started = false
    private var origin: URL?

    public init(options: Options) throws {
        server = try BundleServer(options: options)
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.preferences.javaScriptCanOpenWindowsAutomatically = false
        webView = WKWebView(frame: .zero, configuration: configuration)
        super.init()
        webView.navigationDelegate = self; webView.uiDelegate = self
    }
    /// A runner is single-use. Task cancellation closes the run and releases its listener.
    public func run() async throws -> Result {
        guard !started else { throw Failure.alreadyStarted }
        guard result == nil else { throw Failure.closed }
        started = true
        return await withTaskCancellationHandler(operation: {
            await withCheckedContinuation { continuation in
                waiting = continuation
                server.start(open: { [weak self] url in
                    guard let self, self.result == nil else { return }
                    self.origin = url
                    self.webView.load(URLRequest(url: url))
                }, ready: { [weak self] in self?.onReady?() },
                console: { [weak self] event in self?.onConsole?(event) },
                message: { [weak self] text in self?.onMessage?(text) },
                finished: { [weak self] result in self?.complete(result) })
            }
        }, onCancel: { Task { @MainActor [weak self] in self?.close() } })
    }
    /// Queues a UTF-8 message for window.ext.host.onmessage (256 messages, 24 MiB each).
    public func send(_ message: String) throws { try server.send(message) }
    public func close() { server.finish(status: "closed", reason: "the host closed the run") }
    private func complete(_ value: Result) {
        guard result == nil else { return }
        result = value
        webView.stopLoading()
        webView.navigationDelegate = nil; webView.uiDelegate = nil
        // Tear down executing content, including workers, after recording its result.
        webView.loadHTMLString("", baseURL: nil)
        waiting?.resume(returning: value); waiting = nil
    }
    public func webView(_ view: WKWebView, decidePolicyFor action: WKNavigationAction,
                        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        let url = action.request.url
        let allowed = action.targetFrame?.isMainFrame == true && url?.scheme == "http" &&
            url?.host == "127.0.0.1" && url?.port == origin?.port
        decisionHandler(allowed ? .allow : .cancel)
    }
    public func webView(_ view: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        if (error as NSError).code != NSURLErrorCancelled { server.finish(status: "failed", reason: "the page could not load") }
    }
    public func webView(_ view: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        if (error as NSError).code != NSURLErrorCancelled { server.finish(status: "failed", reason: "the page could not load") }
    }
    public func webViewWebContentProcessDidTerminate(_ view: WKWebView) { server.finish(status: "failed", reason: "web content process terminated") }
    public func webViewDidClose(_ view: WKWebView) { close() }
    deinit { server.stop() }
}
