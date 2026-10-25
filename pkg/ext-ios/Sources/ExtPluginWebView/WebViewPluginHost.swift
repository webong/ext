#if canImport(WebKit)
import Foundation
import WebKit

/// Hosts an ext.plugin/v1 WebAssembly reactor (docs/plugin-reactor-abi.md) in a
/// hidden web view and exchanges frames with it. The web view is the platform's own
/// web engine, so this is the `restricted` route of docs/adr-mobile-wasm.md: the
/// guest has no network, files or process, but it runs under the browser's rules,
/// not a capability sandbox.
///
/// A running WebAssembly call cannot be interrupted, so a call that overruns its
/// deadline destroys the web view and the host is closed. Use one host per plugin.
@MainActor
public final class WebViewPluginHost {
    public enum Failure: Error, Equatable {
        case notStarted
        case closed
        case timeout
        /// The page could not instantiate the module, with the page's reason.
        case page(String)
        /// The guest returned this ext_plugin_call status (1 invalid, 2 closed, 3 failed).
        case status(Int)
    }

    private let module: Data
    private let bridge: String
    private var webView: WKWebView?
    private var handler: Handler?
    private var started = false
    private var closed = false
    private var ready: CheckedContinuation<Void, Error>?
    private var pending: [Int: CheckedContinuation<Data, Error>] = [:]
    private var nextSequence = 1
    private var onLog: ((String) -> Void)?

    /// - Parameters:
    ///   - module: the reactor module's bytes.
    ///   - bridge: the page shim, `res/web/bundle/shim.js`, unchanged, with its
    ///     `%TOKEN%` placeholder in place. Nil uses the copy this package embeds
    ///     (`scripts/plugin-mobile-assets.sh`); pass another to use a newer shim. It
    ///     runs at document start and defines
    ///     `window.ext`; on a `WKWebView` it picks the native transport and posts to
    ///     the script message handler this class registers as `ext`.
    ///   - log: receives the guest's diagnostics and page-side log lines.
    public init(module: Data, bridge: String? = nil, log: ((String) -> Void)? = nil) {
        self.module = module
        self.bridge = bridge ?? GeneratedAssets.shim
        self.onLog = log
    }

    /// Creates the web view, instantiates the module, and waits for the page to be
    /// ready. Throws `Failure.page` if the module is not a usable reactor.
    public func start(timeout: TimeInterval = 30) async throws {
        guard !started, !closed else { throw Failure.closed }
        started = true
        let configuration = WKWebViewConfiguration()
        let handler = Handler(host: self)
        self.handler = handler
        configuration.userContentController.add(handler, name: "ext")
        configuration.userContentController.addUserScript(
            WKUserScript(source: bridge, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        let view = WKWebView(frame: CGRect(x: 0, y: 0, width: 1, height: 1), configuration: configuration)
        webView = view
        view.loadHTMLString(PageGlue.html(moduleBase64: module.base64EncodedString()), baseURL: nil)
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            ready = continuation
            DispatchQueue.main.asyncAfter(deadline: .now() + timeout) { [weak self] in
                Task { @MainActor in self?.expireReady() }
            }
        }
    }

    /// Sends the handshake and returns the descriptor JSON.
    public func handshake(deadline: Date) async throws -> Data {
        let hello = #"{"deadline":"\#(Self.timestamp(deadline))"}"#
        return try await exchange(operation: 1, frame: Data(hello.utf8), timeout: deadline.timeIntervalSinceNow)
    }

    /// Sends one complete ext.plugin/v1 request and returns the complete response.
    public func invoke(_ request: Data, timeout: TimeInterval = 30) async throws -> Data {
        try await exchange(operation: 2, frame: request, timeout: timeout)
    }

    /// Destroys the web view. Pending calls fail with `Failure.closed`. Idempotent.
    public func close() {
        guard !closed else { return }
        closed = true
        webView?.configuration.userContentController.removeScriptMessageHandler(forName: "ext")
        webView?.stopLoading()
        webView = nil
        handler = nil
        ready?.resume(throwing: Failure.closed)
        ready = nil
        let waiting = pending
        pending = [:]
        for continuation in waiting.values { continuation.resume(throwing: Failure.closed) }
    }

    // MARK: - Exchange

    private func exchange(operation: Int, frame: Data, timeout: TimeInterval) async throws -> Data {
        guard started else { throw Failure.notStarted }
        guard !closed, let view = webView else { throw Failure.closed }
        guard timeout > 0 else { close(); throw Failure.timeout }
        let sequence = nextSequence
        nextSequence += 1
        let message = Self.json(["seq": sequence, "op": operation, "frame": String(decoding: frame, as: UTF8.self)])
        return try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Data, Error>) in
            pending[sequence] = continuation
            view.evaluateJavaScript("window.ext.host._deliver(\(Self.literal(message)))") { [weak self] _, error in
                if let error {
                    Task { @MainActor in self?.fail(sequence, Failure.page(error.localizedDescription)) }
                }
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + timeout) { [weak self] in
                Task { @MainActor in
                    // The call overran its deadline. It cannot be interrupted, so the
                    // page is destroyed, which also fails every other pending call.
                    if self?.pending[sequence] != nil {
                        self?.fail(sequence, Failure.timeout)
                        self?.close()
                    }
                }
            }
        }
    }

    private func fail(_ sequence: Int, _ error: Error) {
        pending.removeValue(forKey: sequence)?.resume(throwing: error)
    }

    private func expireReady() {
        if let waiting = ready {
            ready = nil
            waiting.resume(throwing: Failure.timeout)
            close()
        }
    }

    fileprivate func receive(_ body: Any) {
        guard let text = body as? String, let data = text.data(using: .utf8),
              let envelope = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let kind = envelope["kind"] as? String else { return }
        // The page API's events: host carries a frame from window.ext.host.send; log is
        // console output or window.ext.log; alive, closed and exit need no action here.
        switch kind {
        case "host":
            guard let inner = envelope["data"] as? String, let innerData = inner.data(using: .utf8),
                  let message = try? JSONSerialization.jsonObject(with: innerData) as? [String: Any] else { return }
            handle(message)
        case "log":
            onLog?((envelope["text"] as? String) ?? "")
        default:
            break
        }
    }

    private func handle(_ message: [String: Any]) {
        switch message["kind"] as? String {
        case "ready":
            ready?.resume()
            ready = nil
        case "failed":
            let reason = (message["reason"] as? String) ?? "the page failed"
            if let waiting = ready {
                ready = nil
                waiting.resume(throwing: Failure.page(reason))
            }
            close()
        default:
            guard let sequence = message["seq"] as? Int, let status = message["status"] as? Int else { return }
            if status != 0 {
                fail(sequence, Failure.status(status))
            } else {
                let frame = (message["frame"] as? String) ?? ""
                pending.removeValue(forKey: sequence)?.resume(returning: Data(frame.utf8))
            }
        }
    }

    // MARK: - Encoding

    private static func json(_ object: [String: Any]) -> String {
        let data = (try? JSONSerialization.data(withJSONObject: object)) ?? Data("{}".utf8)
        return String(decoding: data, as: UTF8.self)
    }

    /// A JavaScript string literal for `text`.
    private static func literal(_ text: String) -> String {
        let data = (try? JSONSerialization.data(withJSONObject: [text], options: [])) ?? Data("[\"\"]".utf8)
        let array = String(decoding: data, as: UTF8.self)
        return String(array.dropFirst().dropLast())
    }

    private static func timestamp(_ date: Date) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.string(from: date)
    }

    /// Weakly forwards script messages, because the web view retains its handlers.
    private final class Handler: NSObject, WKScriptMessageHandler {
        weak var host: WebViewPluginHost?
        init(host: WebViewPluginHost) { self.host = host }
        func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
            let body = message.body
            Task { @MainActor in self.host?.receive(body) }
        }
    }
}

#endif
