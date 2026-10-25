import XCTest
@testable import ExtWeb

final class WorkspaceScriptsTests: XCTestCase {
    func testReviewedScopeAndEnabledState() throws {
        var record = UserscriptRecord(target: "page", id: "a", name: "A", matches: ["https://example.com/*"],
            enabled: true, source: "window.test = true;")
        record.revision = Userscript.revision(of: record)
        XCTAssertEqual(try WorkspaceScripts.prepare([record], target: "page").count, 1)
        XCTAssertThrowsError(try WorkspaceScripts.prepare([record], target: "other"))
        XCTAssertThrowsError(try WorkspaceScripts.prepare([record, record], target: "page"))
        record.enabled = false
        XCTAssertEqual(try WorkspaceScripts.prepare([record], target: "page"), [])
        record.source += "window.changed = true;"
        XCTAssertThrowsError(try WorkspaceScripts.prepare([record], target: "page"))
    }
}
