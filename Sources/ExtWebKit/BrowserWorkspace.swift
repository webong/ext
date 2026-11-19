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
        public let navigationGeneration: UInt64
    }

    public let webView: WKWebView
    public let target: String
    public var onStateChange: ((State) -> Void)?
    /// A completed main-page visit. The app decides whether and where to store it.
    public var onVisit: ((State) -> Void)?
    /// Non-HTTP navigation is denied; only user-clicked links are offered to the app.
    public var onExternalURL: ((URL) -> Void)?
    /// Reports a queued store reconciliation on the main actor. False means the previous
    /// registrations were retained; no script source or underlying error is exposed.
    public var onUserscriptsReconciled: ((Bool) -> Void)?
    public var isReconcilingUserscripts: Bool { liveScripts != nil }
    private final class LiveScripts {
        let token: UUID, store: UserscriptStore, id: String, observation: UserscriptStore.Observation
        var records: [UserscriptRecord]
        var pending = false
        init(token: UUID, store: UserscriptStore, id: String, observation: UserscriptStore.Observation, records: [UserscriptRecord]) {
            self.token = token; self.store = store; self.id = id; self.observation = observation; self.records = records
        }
    }
    private var liveScripts: LiveScripts?
    private var observations: [NSKeyValueObservation] = []
    private var closed = false
    private var lastError: String?
    private struct PendingInjection {
        let generation: UInt64
        let continuation: CheckedContinuation<Void, Error>
    }
    private var navigationGeneration: UInt64 = 0
    private var injections: [UUID: PendingInjection] = [:]

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
                Task { @MainActor [weak self] in self?.emit() }
            },
            webView.observe(\.title, options: [.new]) { [weak self] _, _ in
                Task { @MainActor [weak self] in self?.emit() }
            },
        ]
    }

    public var state: State {
        State(url: webView.url?.absoluteString ?? "", title: webView.title ?? "",
            loading: webView.isLoading, progress: webView.estimatedProgress,
            canGoBack: webView.canGoBack, canGoForward: webView.canGoForward, error: lastError,
            navigationGeneration: navigationGeneration)
    }

    public var scriptCapabilities: WebViewCapabilities {
        WebViewCapabilities(
            currentPage: ScriptExecutionCapability(supported: true, frames: ["main"], runAt: ["now"],
                worlds: ["page"], rejectsStaleNavigation: true),
            futureDocuments: ScriptExecutionCapability(supported: true, frames: ["all"],
                runAt: ["document-start"], worlds: ["page"]))
    }

    public func navigate(_ address: String) throws {
        try requireOpen()
        _ = try parseSite(address)
        guard let url = URL(string: address) else { throw WorkspaceError.invalidURL }
        beginNavigation(); webView.load(URLRequest(url: url))
    }
    public func back() throws { try requireOpen(); if webView.canGoBack { beginNavigation(); webView.goBack() } }
    public func forward() throws { try requireOpen(); if webView.canGoForward { beginNavigation(); webView.goForward() } }
    public func reload() throws { try requireOpen(); beginNavigation(); webView.reload() }
    public func stop() throws { try requireOpen(); webView.stopLoading(); emit() }

    /// Reads WebKit's native records for a request URL, including HttpOnly cookies.
    /// Records expose only metadata available from WebKit; they are not portable exports.
    public func cookies(for site: String, completion: @escaping @MainActor @Sendable ([HTTPCookie]) -> Void) throws {
        try requireOpen()
        let url = try parseSite(site)
        webView.configuration.websiteDataStore.httpCookieStore.getAllCookies { cookies in
            completion(cookies.filter { cookie in
                CookieRules.domainMatches(siteHost: url.hostname, cookieDomain: cookie.domain)
                    && CookieRules.pathMatches(requestPath: url.escapedPath, cookiePath: cookie.path)
                    && (!cookie.isSecure || url.scheme == "https")
                    && (cookie.expiresDate == nil || cookie.expiresDate! > Date())
            })
        }
    }

    /// Validates before mutation. Completion means WebKit finished the write, not that a
    /// navigation was authenticated. Accepted operations may complete after close().
    public func setCookie(_ bundle: CookieBundle, completion: @escaping @MainActor @Sendable () -> Void) throws {
        try requireOpen()
        let cookie = try WorkspaceCookies.native(bundle)
        webView.configuration.websiteDataStore.httpCookieStore.setCookie(cookie, completionHandler: completion)
    }

    /// Deletes exactly a native record returned by cookies(for:completion:).
    public func deleteCookie(_ cookie: HTTPCookie, completion: @escaping @MainActor @Sendable () -> Void) throws {
        try requireOpen()
        webView.configuration.websiteDataStore.httpCookieStore.delete(cookie, completionHandler: completion)
    }

    /// Replaces registrations for FUTURE documents, in page world and all frames.
    /// Removing a script cannot undo its effects in an already-loaded document.
    public func replaceUserscripts(_ records: [UserscriptRecord]) throws {
        try requireOpen()
        try applyUserscripts(records)
        stopUserscriptReconciliation()
    }
    private func applyUserscripts(_ records: [UserscriptRecord]) throws {
        let sources = try WorkspaceScripts.prepare(records, target: target)
        let controller = webView.configuration.userContentController
        controller.removeAllUserScripts()
        for source in sources {
            controller.addUserScript(WKUserScript(source: source, injectionTime: .atDocumentStart,
                forMainFrameOnly: false))
        }
    }

    /// Registers the selected enabled scripts and observes subsequent store API writes.
    /// Initial selection/registration failure leaves the previous binding and scripts intact.
    @discardableResult public func activateUserscripts(store: UserscriptStore, id: String = "") throws -> Int {
        try requireOpen()
        let token = UUID()
        let observation = try store.observe(target: target) { [weak self] in
            DispatchQueue.main.async { [weak self] in self?.scheduleReconciliation(token) }
        }
        do {
            let records = try selectedRecords(store, id: id)
            guard id.isEmpty || !records.isEmpty else { throw WorkspaceError.invalidUserscriptSelection }
            try applyUserscripts(records)
            stopUserscriptReconciliation()
            liveScripts = LiveScripts(token: token, store: store, id: id, observation: observation, records: records)
            return records.count
        } catch { observation.cancel(); throw error }
    }
    /// Stops synchronization, retaining the current registrations until replaced or closed.
    public func stopUserscriptReconciliation() {
        liveScripts?.observation.cancel()
        liveScripts = nil
    }
    private func selectedRecords(_ store: UserscriptStore, id: String) throws -> [UserscriptRecord] {
        try store.list(target: target).filter { $0.enabled && (id.isEmpty || $0.id == id) }
    }
    private func scheduleReconciliation(_ token: UUID) {
        guard !closed, let live = liveScripts, live.token == token, !live.pending else { return }
        live.pending = true
        DispatchQueue.main.async { [weak self] in self?.reconcile(token) }
    }
    private func reconcile(_ token: UUID) {
        guard !closed, let live = liveScripts, live.token == token else { return }
        live.pending = false
        do {
            let records = try selectedRecords(live.store, id: live.id)
            if records != live.records { try applyUserscripts(records); live.records = records }
        } catch { onUserscriptsReconciled?(false); return }
        onUserscriptsReconciled?(true)
    }

    public func close() {
        guard !closed else { return }
        closed = true
        stopUserscriptReconciliation()
        onUserscriptsReconciled = nil
        let pending = Array(injections.values); injections.removeAll()
        pending.forEach { $0.continuation.resume(throwing: WorkspaceError.closed) }
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
        beginNavigation(); lastError = nil; emit()
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
        beginNavigation(); lastError = "Web content process terminated"; emit()
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
    func checkOpen() throws { try requireOpen() }
    /// Executes reviewed source in the current main frame's page world. Completion means
    /// synchronous evaluation finished; promises returned by the script are not awaited.
    public func inject(_ source: String) async throws {
        try requireOpen()
        guard !source.isEmpty, source.utf8.count <= 1 << 20 else { throw WorkspaceError.injectionFailed }
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let id = UUID()
            let generation = navigationGeneration
            injections[id] = PendingInjection(generation: generation, continuation: continuation)
            webView.evaluateJavaScript(source + "\n;void 0;") { [weak self] _, error in
                guard let self, let pending = self.injections.removeValue(forKey: id) else { return }
                if pending.generation != self.navigationGeneration {
                    pending.continuation.resume(throwing: WorkspaceError.staleNavigation)
                } else if error != nil {
                    pending.continuation.resume(throwing: WorkspaceError.injectionFailed)
                } else {
                    pending.continuation.resume()
                }
            }
        }
    }
    private func beginNavigation() {
        navigationGeneration &+= 1
        let pending = Array(injections.values); injections.removeAll()
        pending.forEach { $0.continuation.resume(throwing: WorkspaceError.staleNavigation) }
    }
    private func requireOpen() throws { if closed { throw WorkspaceError.closed } }
    public enum WorkspaceError: Error { case closed, invalidURL, injectionFailed, staleNavigation, invalidUserscriptSelection }
}
