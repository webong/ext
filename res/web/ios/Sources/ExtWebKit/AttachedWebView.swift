import ExtWeb
import Foundation
@preconcurrency import WebKit

/// Script and cookie operations on a caller-owned view. The caller owns the
/// view, its configuration and delegates, and the decision to run reviewed code.
@MainActor
public final class AttachedWebView {
    public enum AttachmentError: Error {
        case closed, documentStartUnavailable, injectionFailed, staleNavigation
    }

    public let target: String
    private weak var view: WKWebView?
    private weak var scriptController: WKUserContentController?
    private var scripts: [WKUserScript] = []
    private struct Injection {
        let continuation: CheckedContinuation<Void, Error>
        let timeout: Task<Void, Never>
    }
    private var injections: [UUID: Injection] = [:]

    /// Enable future scripts only with the view's actual controller and only if
    /// it belongs exclusively to this view. WebKit applies scripts to every view
    /// sharing a controller and provides no public way to check for sharing.
    public init(_ view: WKWebView, target: String, exclusiveScriptController: WKUserContentController? = nil) {
        self.view = view
        self.target = target
        self.scriptController = exclusiveScriptController
    }

    public var scriptCapabilities: WebViewCapabilities {
        let attached = view != nil
        let future = view.map { $0.configuration.userContentController === scriptController } ?? false
        return WebViewCapabilities(
            currentPage: ScriptExecutionCapability(supported: attached, frames: attached ? ["main"] : [],
                runAt: attached ? ["now"] : [], worlds: attached ? ["page"] : [], rejectsStaleNavigation: attached),
            futureDocuments: ScriptExecutionCapability(supported: future, frames: future ? ["all"] : [],
                runAt: future ? ["document-start"] : [], worlds: future ? ["page"] : []))
    }

    /// Replaces this attachment's registrations for future documents. Host
    /// scripts and message handlers retain their identities and relative order.
    public func replaceUserscripts(_ records: [UserscriptRecord]) throws {
        let view = try requireView()
        let sources = try WorkspaceScripts.prepare(records, target: target)
        if sources.isEmpty && scripts.isEmpty { return }
        guard let controller = scriptController, controller === view.configuration.userContentController else {
            throw AttachmentError.documentStartUnavailable
        }
        removeScripts()
        scripts = sources.map { WKUserScript(source: $0, injectionTime: .atDocumentStart, forMainFrameOnly: false) }
        scripts.forEach(controller.addUserScript)
    }

    private func removeScripts() {
        defer { scripts.removeAll() }
        guard !scripts.isEmpty, let controller = scriptController else { return }
        let owned = Set(scripts.map(ObjectIdentifier.init))
        let existing = controller.userScripts
        let retained = existing.filter { !owned.contains(ObjectIdentifier($0)) }
        guard retained.count != existing.count else { return }
        // WebKit's public API removes scripts as a group. Restore the live
        // list minus our own entries, including scripts the host added later.
        controller.removeAllUserScripts()
        retained.forEach(controller.addUserScript)
    }

    /// Reads native records from the attached view's website data store.
    public func cookies(for site: String, completion: @escaping @MainActor @Sendable ([HTTPCookie]) -> Void) throws {
        let view = try requireView()
        let url = try parseSite(site)
        view.configuration.websiteDataStore.httpCookieStore.getAllCookies { cookies in
            let now = Date()
            completion(cookies.filter { cookie in
                CookieRules.domainMatches(siteHost: url.hostname, cookieDomain: cookie.domain)
                    && CookieRules.pathMatches(requestPath: url.escapedPath, cookiePath: cookie.path)
                    && (!cookie.isSecure || url.scheme == "https")
                    && (cookie.expiresDate.map { $0 > now } ?? true)
            })
        }
    }

    /// Accepted cookie operations may finish after the attachment is closed.
    public func setCookie(_ bundle: CookieBundle, completion: @escaping @MainActor @Sendable () -> Void) throws {
        let view = try requireView()
        let cookie = try WorkspaceCookies.native(bundle)
        view.configuration.websiteDataStore.httpCookieStore.setCookie(cookie, completionHandler: completion)
    }

    /// Deletes exactly a native record from this store.
    public func deleteCookie(_ cookie: HTTPCookie, completion: @escaping @MainActor @Sendable () -> Void) throws {
        let view = try requireView()
        view.configuration.websiteDataStore.httpCookieStore.delete(cookie, completionHandler: completion)
    }

    /// Executes reviewed source in the current main frame's page world.
    /// Completion covers synchronous evaluation. Promises are not awaited.
    public func inject(_ source: String) async throws {
        let view = try requireView()
        guard !source.isEmpty, source.utf8.count <= 1 << 20, injections.count < 64 else {
            throw AttachmentError.injectionFailed
        }
        let url = view.url
        let id = UUID()
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let timeout = Task { @MainActor [weak self] in
                do { try await Task.sleep(nanoseconds: 30_000_000_000) } catch { return }
                self?.finish(id, error: .injectionFailed)
            }
            injections[id] = Injection(continuation: continuation, timeout: timeout)
            view.evaluateJavaScript(source + "\n;void 0;") { [weak self] _, error in
                guard let self else { return }
                guard let current = self.view else { self.finish(id, error: .closed); return }
                if current.url != url { self.finish(id, error: .staleNavigation) }
                else { self.finish(id, error: error == nil ? nil : .injectionFailed) }
            }
        }
    }

    /// Forward main-frame navigation starts and renderer invalidation from the
    /// host, including same-URL reloads. Submitted JavaScript cannot be recalled.
    public func invalidateNavigation() {
        for id in Array(injections.keys) { finish(id, error: .staleNavigation) }
    }

    /// Detaches without stopping navigation or destroying the view.
    public func close() {
        view = nil
        removeScripts()
        scriptController = nil
        for id in Array(injections.keys) { finish(id, error: .closed) }
    }

    private func requireView() throws -> WKWebView {
        guard let view else { throw AttachmentError.closed }
        return view
    }

    private func finish(_ id: UUID, error: AttachmentError?) {
        guard let pending = injections.removeValue(forKey: id) else { return }
        pending.timeout.cancel()
        if let error { pending.continuation.resume(throwing: error) }
        else { pending.continuation.resume() }
    }
}
