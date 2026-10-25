import XCTest
import ExtWeb
@testable import ExtWebKit

@MainActor
final class BrowserWorkspaceTests: XCTestCase {
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
        defer { workspace.close() }
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

    func testDuplicateAndTargetValidation() throws {
        let workspace = BrowserWorkspace(target: "other")
        defer { workspace.close() }
        XCTAssertThrowsError(try workspace.replaceUserscripts([record()]))
        XCTAssertThrowsError(try WorkspaceScripts.prepare([record(), record()], target: "test"))
        XCTAssertThrowsError(try workspace.navigate("file:///etc/passwd"))
    }
}
