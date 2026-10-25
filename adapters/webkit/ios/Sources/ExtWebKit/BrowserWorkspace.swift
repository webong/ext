import Foundation
import ExtWeb
@preconcurrency import WebKit

/// A host-owned WebKit page. All access, including callbacks, is on the main actor.
/// Embed `webView` in your UI; do not replace its delegates or user-content controller.
@MainActor
public final class BrowserWorkspace: NSObject, WKNavigationDelegate, WKUIDelegate {
    public struct State {
        public let url: String
        public let title: String
        public let loading: Bool
        public let progress: Double
        public let canGoBack: Bool
        public let canGoForward: Bool
        public let error: String?
    }

    public let webView: WKWebView
    public let target: String
    public var onStateChange: ((State) -> Void)?
    /// A completed main-page visit. The app decides whether and where to store it.
    public var onVisit: ((State) -> Void)?
    /// Non-HTTP navigation is denied; only user-clicked links are offered to the app.
    public var onExternalURL: ((URL) -> Void)?
    private var observations: [NSKeyValueObservation] = []
    private var closed = false
    private var lastError: String?

    public init(target: String, dataStore: WKWebsiteDataStore? = nil) {
        self.target = target
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = dataStore ?? .nonPersistent()
        configuration.preferences.javaScriptCanOpenWindowsAutomatically = false
        webView = WKWebView(frame: .zero, configuration: configuration)
        super.init()
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.allowsBackForwardNavigationGestures = true
        observations = [
            webView.observe(\.estimatedProgress, options: [.new]) { [weak self] _, _ in
                Task { @MainActor in self?.emit() }
            },
            webView.observe(\.title, options: [.new]) { [weak self] _, _ in
                Task { @MainActor in self?.emit() }
            },
        ]
    }

    public var state: State {
        State(url: webView.url?.absoluteString ?? "", title: webView.title ?? "",
            loading: webView.isLoading, progress: webView.estimatedProgress,
            canGoBack: webView.canGoBack, canGoForward: webView.canGoForward, error: lastError)
    }

    public func navigate(_ address: String) throws {
        try requireOpen()
        _ = try parseSite(address)
        guard let url = URL(string: address) else { throw WorkspaceError.invalidURL }
        webView.load(URLRequest(url: url))
    }
    public func back() throws { try requireOpen(); webView.goBack() }
    public func forward() throws { try requireOpen(); webView.goForward() }
    public func reload() throws { try requireOpen(); webView.reload() }
    public func stop() throws { try requireOpen(); webView.stopLoading(); emit() }

    /// Replaces registrations for FUTURE documents, in page world and all frames.
    /// Removing a script cannot undo its effects in an already-loaded document.
    public func replaceUserscripts(_ records: [UserscriptRecord]) throws {
        try requireOpen()
        let sources = try WorkspaceScripts.prepare(records, target: target)
        let controller = webView.configuration.userContentController
        controller.removeAllUserScripts()
        for source in sources {
            controller.addUserScript(WKUserScript(source: source, injectionTime: .atDocumentStart,
                forMainFrameOnly: false))
        }
    }

    public func close() {
        guard !closed else { return }
        closed = true
        observations.forEach { $0.invalidate() }
        observations.removeAll()
        webView.stopLoading()
        webView.configuration.userContentController.removeAllUserScripts()
        webView.navigationDelegate = nil
        webView.uiDelegate = nil
        // Drop the active document as well as future registrations.
        webView.loadHTMLString("", baseURL: nil)
        onStateChange = nil; onVisit = nil; onExternalURL = nil
    }

    public func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
        lastError = nil; emit()
    }
    public func webView(_ webView: WKWebView, didCommit navigation: WKNavigation!) { emit() }
    public func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard !closed else { return }
        emit()
        if lastError == nil, let address = webView.url?.absoluteString, (try? parseSite(address)) != nil {
            onVisit?(state)
        }
    }
    public func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        lastError = error.localizedDescription; emit()
    }
    public func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        lastError = error.localizedDescription; emit()
    }
    public func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        lastError = "Web content process terminated"; emit()
    }
    public func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard !closed, let url = action.request.url else { decisionHandler(.cancel); return }
        if ["http", "https"].contains(url.scheme?.lowercased() ?? "") || url.absoluteString == "about:blank" {
            decisionHandler(.allow)
        } else {
            decisionHandler(.cancel)
            if action.navigationType == .linkActivated { onExternalURL?(url) }
        }
    }
    public func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                        for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if !closed, action.targetFrame == nil, let url = action.request.url,
            ["http", "https"].contains(url.scheme?.lowercased() ?? "") {
            webView.load(URLRequest(url: url))
        }
        return nil
    }
    private func emit() { if !closed { onStateChange?(state) } }
    private func requireOpen() throws { if closed { throw WorkspaceError.closed } }
    public enum WorkspaceError: Error { case closed, invalidURL }
}
