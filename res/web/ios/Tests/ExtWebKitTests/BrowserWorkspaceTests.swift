import XCTest
#if canImport(UIKit)
import UIKit
#endif
import ExtWeb
@testable import ExtWebKit

@MainActor
final class BrowserWorkspaceTests: XCTestCase {
    #if canImport(UIKit)
    private var testWindow: UIWindow?
    #endif

    private func attach(_ workspace: BrowserWorkspace) {
        #if canImport(UIKit)
        let window = UIWindow(frame: UIScreen.main.bounds)
        let controller = UIViewController()
        controller.view = workspace.webView
        window.rootViewController = controller
        window.makeKeyAndVisible()
        testWindow = window
        #endif
    }

    private func detach() {
        #if canImport(UIKit)
        testWindow?.isHidden = true
        testWindow = nil
        #endif
    }

    private func record(_ source: String = "globalThis.runs = (globalThis.runs || 0) + 1;") -> UserscriptRecord {
        var r = UserscriptRecord(target: "test", id: "script", name: "Script",
            matches: ["https://example.com/*"], excludeMatches: ["https://example.com/private/*"],
            enabled: true, source: source)
        r.revision = Userscript.revision(of: r)
        return r
    }

    private func load(_ workspace: BrowserWorkspace, path: String) async throws -> Any? {
        workspace.webView.loadHTMLString("<html><script>window.atStart = window.runs || 0; window.ready = true;</script></html>",
            baseURL: URL(string: "https://example.com" + path))
        for _ in 0..<200 {
            try await Task.sleep(nanoseconds: 50_000_000)
            if (try? await workspace.webView.evaluateJavaScript("window.ready === true")) as? Bool == true,
                !workspace.webView.isLoading {
                return try await workspace.webView.evaluateJavaScript("window.atStart")
            }
        }
        XCTFail("page did not load")
        return nil
    }

    func testDocumentStartScopeReplacementAndClose() async throws {
        let workspace = BrowserWorkspace(target: "test")
        attach(workspace)
        defer { workspace.close(); detach() }
        try workspace.replaceUserscripts([record()])
        let first = try await load(workspace, path: "/public")
        XCTAssertEqual(first as? Int, 1, "userscript must run before inline page code")
        let excluded = try await load(workspace, path: "/private/secret")
        XCTAssertEqual(excluded as? Int, 0)
        var tampered = record(); tampered.source = "throw 'tampered';"
        XCTAssertThrowsError(try workspace.replaceUserscripts([tampered]))
        let kept = try await load(workspace, path: "/public-again")
        XCTAssertEqual(kept as? Int, 1, "rejected update must preserve previous registrations")
        try workspace.replaceUserscripts([])
        let removed = try await load(workspace, path: "/removed")
        XCTAssertEqual(removed as? Int, 0)
        workspace.close()
        XCTAssertThrowsError(try workspace.navigate("https://example.com"))
    }

    func testNativeCookieStore() async throws {
        let workspace = BrowserWorkspace(target: "cookies")
        defer { workspace.close() }
        let cookie = Cookie(name: "ext_cookie_test", value: "fixture-token", domain: "example.com", path: "/account",
            expiry: Int64(Date().timeIntervalSince1970) + 3600, secure: true, httpOnly: true, sameSitePolicy: "strict")
        let bundle = try CookieWorkflows.bundle(cookie, site: "https://example.com/account")
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            do { try workspace.setCookie(bundle) { continuation.resume() } }
            catch { continuation.resume(throwing: error) }
        }
        func read(_ site: String) async throws -> [HTTPCookie] {
            try await withCheckedThrowingContinuation { continuation in
                do { try workspace.cookies(for: site) { continuation.resume(returning: $0) } }
                catch { continuation.resume(throwing: error) }
            }
        }
        let found = try await read("https://example.com/account/page")
        let native = try XCTUnwrap(found.first { $0.name == cookie.name })
        XCTAssertTrue(native.value == cookie.value, "cookie value round trips")
        XCTAssertTrue(native.isHTTPOnly)
        XCTAssertTrue(native.isSecure)
        XCTAssertEqual(native.sameSitePolicy?.rawValue.lowercased(), "strict")
        XCTAssertEqual(native.expiresDate?.timeIntervalSince1970, Double(cookie.expiry))
        for site in ["http://example.com/account", "https://sub.example.com/account", "https://example.com/other"] {
            let excluded = try await read(site)
            XCTAssertFalse(excluded.contains { $0.name == cookie.name }, "scope must be enforced")
        }
        var isolated = bundle; isolated.cookie.partitionKey = "https://top.example"
        XCTAssertThrowsError(try workspace.setCookie(isolated) {})
        var injection = bundle; injection.cookie.path = "/account; Domain=other.com"
        XCTAssertThrowsError(try workspace.setCookie(injection) {})
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            do { try workspace.deleteCookie(native) { continuation.resume() } }
            catch { continuation.resume(throwing: error) }
        }
        let deleted = try await read(bundle.site)
        XCTAssertFalse(deleted.contains { $0.name == cookie.name })
        workspace.close()
        XCTAssertThrowsError(try workspace.cookies(for: bundle.site) { _ in })
        XCTAssertThrowsError(try workspace.setCookie(bundle) {})
    }

    func testDuplicateAndTargetValidation() throws {
        let workspace = BrowserWorkspace(target: "other")
        defer { workspace.close() }
        XCTAssertThrowsError(try workspace.replaceUserscripts([record()]))
        XCTAssertThrowsError(try WorkspaceScripts.prepare([record(), record()], target: "test"))
        XCTAssertThrowsError(try workspace.navigate("file:///etc/passwd"))
    }
}
