import Foundation
import XCTest
@testable import ExtWeb

final class CookieWorkflowTests: XCTestCase {
    struct Case: Decodable {
        let name: String, input: String, valid: Bool, site: String
        let cookies: [Cookie]?, header: String?, header_error: Bool, jar: String?
    }
    func testSharedWorkflows() throws {
        for c in try Fixtures.load("cookie/v2/workflows.json", as: [Case].self) {
            do {
                let cookies = try CookieWorkflows.parse(Data(c.input.utf8))
                XCTAssertTrue(c.valid, c.name)
                XCTAssertEqual(cookies, c.cookies, c.name)
                if let header = c.header { XCTAssertEqual(try CookieWorkflows.header(cookies, site: c.site), header, c.name) }
                if c.header_error { XCTAssertThrowsError(try CookieWorkflows.header(cookies, site: c.site), c.name) }
                if let jar = c.jar { XCTAssertEqual(try CookieWorkflows.netscape(cookies), jar, c.name) }
            } catch {
                XCTAssertFalse(c.valid, c.name + ": " + String(describing: error))
                XCTAssertFalse(String(describing: error).contains("fixture-token"))
            }
        }
    }
    func testLimitsBundleAndIsolation() throws {
        XCTAssertThrowsError(try CookieWorkflows.parse(Data([0xff])))
        XCTAssertThrowsError(try CookieWorkflows.parse(Data(String(repeating: "[", count: 129).utf8)))
        XCTAssertThrowsError(try CookieWorkflows.parse(Data(repeating: 32, count: CookieWorkflows.maxInputBytes + 1)))
        let cookie = Cookie(name: "sid", value: "fixture-token", domain: "example.com", path: "/")
        let bundle = try CookieWorkflows.bundle(cookie, site: "https://example.com/a?token=x#fragment")
        XCTAssertEqual(bundle.site, "https://example.com/a")
        XCTAssertEqual(bundle.source, "inline")
        XCTAssertThrowsError(try CookieWorkflows.bundle(cookie, site: "https://other.com/"))
        var isolated = cookie; isolated.partitionKey = "https://top.example"
        XCTAssertThrowsError(try CookieWorkflows.netscape([isolated]))
        let jar = try CookieWorkflows.netscape([cookie])
        XCTAssertEqual(try CookieWorkflows.parse(Data(jar.utf8)), [cookie])
    }
}
