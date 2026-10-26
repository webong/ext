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

    func testScriptCapabilitiesAndNavigationGeneration() throws {
        let workspace = BrowserWorkspace(target: "capabilities")
        defer { workspace.close() }
        let capabilities = workspace.scriptCapabilities
        XCTAssertEqual(capabilities.currentPage.frames, ["main"])
        XCTAssertEqual(capabilities.currentPage.runAt, ["now"])
        XCTAssertTrue(capabilities.currentPage.rejectsStaleNavigation)
        XCTAssertEqual(capabilities.futureDocuments.frames, ["all"])
        XCTAssertEqual(capabilities.futureDocuments.runAt, ["document-start"])
        let before = workspace.state.navigationGeneration
        try workspace.navigate("https://example.com/")
        XCTAssertGreaterThan(workspace.state.navigationGeneration, before)
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

    func testManagementRoutingAndPersistence() async throws {
        let workspace = BrowserWorkspace(target: "test")
        attach(workspace)
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { workspace.close(); detach(); try? FileManager.default.removeItem(at: root) }
        let store = UserscriptStore(directory: root)
        let router = WorkspaceManagement(workspace: workspace, store: store, profile: "profile")
        func call(_ kind: String, _ action: String, _ input: String = "{}") async throws -> BrowserManagement.Response {
            try await router.manage(profile: "profile", request: .init(kind: kind, action: action, input: input))
        }
        let r = record()
        let payload = "{\"id\":\"script\",\"name\":\"Script\",\"matches\":[\"https://example.com/*\"],\"excludeMatches\":[\"https://example.com/private/*\"],\"source\":" + String(decoding: try JSONEncoder().encode(r.source), as: UTF8.self) + "}"
        let prepared = try await call("userscript", "prepare", payload)
        XCTAssertEqual(prepared.status, "prepared")
        XCTAssertEqual(try store.list(target: "test").count, 0)
        _ = try await call("userscript", "install", payload)
        XCTAssertEqual(try UserscriptStore(directory: root).load(target: "test", id: "script"), r)
        let targets = try await call("session", "targets")
        XCTAssertTrue(targets.result!.contains("webview"))
        _ = try await call("session", "connect", "{\"target\":" + targets.result!.dropFirst().dropLast() + "}")
        _ = try await call("userscript", "activate")
        let loaded = try await load(workspace, path: "/routed")
        XCTAssertEqual(loaded as? Int, 1)
        let injected = try await call("session", "inject", #"{"source":"window.routed = 42;","options":{"runAt":"now","world":"page"}}"#)
        XCTAssertEqual(injected.status, "injected")
        let value = try await workspace.webView.evaluateJavaScript("window.routed")
        XCTAssertEqual(value as? Int, 42)
        do { _ = try await call("session", "inject", #"{"source":"throw new Error('fixture');"}"#); XCTFail("execution failure accepted") } catch {}
        do { _ = try await call("session", "navigate", #"{"url":"file:///etc/passwd"}"#); XCTFail("invalid navigation accepted") } catch {}
        do { _ = try await call("session", "endpoint"); XCTFail("unsupported endpoint accepted") } catch {}
        do { _ = try await router.manage(profile: "other", request: .init(kind: "userscript", action: "uninstall", input: #"{"id":"script"}"#)); XCTFail("profile mismatch") } catch {}
        var tampered = r; tampered.source += "tampered"
        let replay = "{\"records\":" + String(decoding: try JSONEncoder().encode([tampered]), as: UTF8.self) + "}"
        do { _ = try await call("session", "replay", replay); XCTFail("tampered replay accepted") } catch {}
        let kept = try await load(workspace, path: "/kept")
        XCTAssertEqual(kept as? Int, 1)
        _ = try await call("userscript", "disable", #"{"id":"script"}"#)
        _ = try await call("userscript", "activate")
        let disabled = try await load(workspace, path: "/disabled")
        XCTAssertEqual(disabled as? Int, 0)
        _ = try await call("userscript", "enable", #"{"id":"script"}"#)
        let restored = try UserscriptStore(directory: root).load(target: "test", id: "script")
        _ = try await call("session", "replay", "{\"records\":" + String(decoding: try JSONEncoder().encode([restored]), as: UTF8.self) + "}")
        let replayed = try await load(workspace, path: "/replayed")
        XCTAssertEqual(replayed as? Int, 1)
        _ = try await call("userscript", "uninstall", #"{"id":"script"}"#)
        _ = try await call("userscript", "activate")
        let removed = try await load(workspace, path: "/removed")
        XCTAssertEqual(removed as? Int, 0)
        workspace.close()
        do { _ = try await call("session", "targets"); XCTFail("closed workspace accepted") } catch {}
    }

    func testLiveUserscriptReconciliation() async throws {
        let workspace = BrowserWorkspace(target: "test")
        attach(workspace)
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { workspace.close(); detach(); try? FileManager.default.removeItem(at: root) }
        let store = UserscriptStore(directory: root), writer = UserscriptStore(directory: root)
        let router = WorkspaceManagement(workspace: workspace, store: store, profile: "profile")
        try store.save(record())
        _ = try await router.manage(profile: "profile", request: .init(kind: "userscript", action: "activate", input: "{}"))
        XCTAssertTrue(workspace.isReconcilingUserscripts)
        func changed(success: Bool = true, _ mutation: () async throws -> Void) async throws {
            let event = expectation(description: "reconciled")
            workspace.onUserscriptsReconciled = { ok in XCTAssertEqual(ok, success); event.fulfill() }
            try await mutation()
            await fulfillment(of: [event], timeout: 3)
            workspace.onUserscriptsReconciled = nil
        }
        func runs(_ expected: Int) async throws {
            let value = try await load(workspace, path: "/" + UUID().uuidString)
            XCTAssertEqual(value as? Int, expected)
        }
        try await runs(1)
        let updated = record("globalThis.runs = 2;")
        try await changed {
            try await Task.detached { try writer.save(updated, replace: true) }.value
        }
        try await runs(2)
        try await changed { _ = try writer.setEnabled(target: "test", id: "script", enabled: false) }
        try await runs(0)
        try await changed { _ = try writer.setEnabled(target: "test", id: "script", enabled: true) }
        try await runs(2)
        try await changed { try writer.remove(target: "test", id: "script") }
        try await runs(0)
        try await changed { try writer.save(record()) }
        try await runs(1)

        // A selected-ID binding remains live through disable/removal and reinstatement.
        XCTAssertEqual(try workspace.activateUserscripts(store: store, id: "script"), 1)
        var other = record("globalThis.runs = 100;"); other.id = "other"; other.revision = Userscript.revision(of: other)
        try await changed { try writer.save(other) }
        try await runs(1)
        try await changed { _ = try writer.setEnabled(target: "test", id: "script", enabled: false) }
        try await runs(0)
        try await changed { try writer.remove(target: "test", id: "script") }
        try await runs(0)
        try await changed { try writer.save(record()) }
        try await runs(1)
        XCTAssertThrowsError(try workspace.activateUserscripts(store: store, id: "missing"))
        var invalid = record(); invalid.source = "tampered"
        XCTAssertThrowsError(try workspace.replaceUserscripts([invalid]))
        XCTAssertTrue(workspace.isReconcilingUserscripts)

        // A corrupt disk record discovered on the next notification fails without replacing
        // the working registration. A subsequent valid write recovers the same binding.
        let folder = try XCTUnwrap(FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil).first)
        try Data("{}".utf8).write(to: folder.appendingPathComponent("script.json"))
        try await changed(success: false) { try writer.save(other, replace: true) }
        try await runs(1)
        try await changed { try writer.save(updated, replace: true) }
        try await runs(2)

        let stale = expectation(description: "stopped notifications")
        stale.isInverted = true
        workspace.onUserscriptsReconciled = { _ in stale.fulfill() }
        try writer.save(record(), replace: true) // notification queued before explicit replay
        try workspace.replaceUserscripts([])
        XCTAssertFalse(workspace.isReconcilingUserscripts)
        await fulfillment(of: [stale], timeout: 0.2)
        try await runs(0)
        let closedEvent = expectation(description: "closed notifications")
        closedEvent.isInverted = true
        workspace.onUserscriptsReconciled = { _ in closedEvent.fulfill() }
        try workspace.activateUserscripts(store: store, id: "script")
        try writer.save(updated, replace: true)
        workspace.close()
        XCTAssertFalse(workspace.isReconcilingUserscripts)
        XCTAssertThrowsError(try workspace.activateUserscripts(store: store))
        try writer.save(record(), replace: true)
        await fulfillment(of: [closedEvent], timeout: 0.2)
    }

    func testCloseRejectsPendingInjection() async throws {
        let workspace = BrowserWorkspace(target: "closing")
        var closed = false
        let injection = Task {
            try await workspace.inject("window.pending = true;")
            return !closed
        }
        await Task.yield()
        closed = true; workspace.close()
        // Evaluation may finish before close, but can never succeed afterwards.
        do { let finishedBeforeClose = try await injection.value; XCTAssertTrue(finishedBeforeClose) } catch {}
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
