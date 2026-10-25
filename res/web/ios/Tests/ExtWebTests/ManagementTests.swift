import Foundation
import XCTest
@testable import ExtWeb

final class ManagementTests: XCTestCase {
    typealias M = BrowserManagement
    struct Fixture: Decodable {
        let operations: [String: [String]]
        let statuses: [String]
        let cases: [Case]
        struct Case: Decodable {
            let name: String
            let request: String
            let valid: Bool
            let response: String?
            let response_valid: Bool?
        }
    }
    func testSharedGoFixtures() throws {
        let f = try Fixtures.load("management/v1/protocol.json", as: Fixture.self)
        XCTAssertEqual(f.operations, M.operations)
        for status in f.statuses { XCTAssertEqual(M.persistent(status), status == "installed") }
        for c in f.cases {
            let request: M.Request
            do { request = try .decode(c.request) }
            catch { XCTAssertFalse(c.valid, c.name); continue }
            XCTAssertTrue(c.valid, c.name)
            XCTAssertEqual(try M.Request.decode(request.json()).input, request.input, c.name)
            if let json = c.response {
                do {
                    let response = try M.Response.decode(json, for: request)
                    XCTAssertEqual(c.response_valid, true, c.name)
                    XCTAssertEqual(try M.Response.decode(response.json(for: request), for: request).result, response.result, c.name)
                } catch { XCTAssertEqual(c.response_valid, false, c.name) }
            }
        }
    }
    func testDispatchBoundaries() throws {
        var called = false
        let invalid = M.Request(kind: "userscript", action: "unknown")
        XCTAssertThrowsError(try M.dispatch(profile: "profile", request: invalid) { _, request in
            called = true; return M.Response(request: request, status: "ready")
        })
        XCTAssertFalse(called)
        let request = M.Request(kind: "userscript", action: "list", input: "{\"n\":1e999}")
        let response = try M.dispatch(profile: "profile", request: request) { profile, received in
            XCTAssertEqual(profile, "profile"); XCTAssertEqual(received.input, request.input)
            return M.Response(request: received, status: "future", result: "[1e999]")
        }
        XCTAssertEqual(response.result, "[1e999]")
        XCTAssertThrowsError(try M.dispatch(profile: "", request: request) { _, received in
            M.Response(request: received, status: "")
        })
        enum BackendError: Error { case failed }
        XCTAssertThrowsError(try M.dispatch(profile: "", request: request) { _, _ in throw BackendError.failed }) { error in
            XCTAssertTrue(error is BackendError)
        }
    }
    func testAsyncDispatch() async throws {
        let request = M.Request(kind: "session", action: "navigate")
        let response = try await M.dispatch(profile: "p", request: request) { _, received async throws in
            M.Response(request: received, status: M.Status.navigated)
        }
        XCTAssertEqual(response.status, "navigated")
    }
    func testBoundsAndRedaction() throws {
        let request = M.Request(kind: "userscript", action: "list")
        let maxInput = "{\"x\":\"" + String(repeating: "a", count: M.maxRequestInputBytes - 8) + "\"}"
        XCTAssertEqual(maxInput.utf8.count, M.maxRequestInputBytes)
        try M.Request(kind: "userscript", action: "list", input: maxInput).validate()
        XCTAssertThrowsError(try M.Request(kind: "userscript", action: "list", input: maxInput + " ").validate())
        let result = "\"" + String(repeating: "a", count: M.maxResultBytes - 2) + "\""
        try M.Response(request: request, status: "ready", result: result).validate(for: request)
        XCTAssertThrowsError(try M.Response(request: request, status: "ready", result: result + " ").validate(for: request))
        XCTAssertThrowsError(try M.Response.decode(String(repeating: " ", count: M.maxResponseBytes + 1), for: request))
        XCTAssertThrowsError(try M.Response(request: request, status: "ready", result: String(repeating: "[", count: 129) + "0" + String(repeating: "]", count: 129)).validate(for: request))
        let endpoint = M.Endpoint(protocol: "cdp", endpoint: "ws://localhost:9/secret-token")
        XCTAssertFalse(String(reflecting: endpoint).contains("secret-token"))
        XCTAssertTrue(try endpoint.json().contains("secret-token"))
        XCTAssertFalse(String(reflecting: M.Response(request: request, status: "ready", result: "\"secret-token\"")).contains("secret-token"))
    }
}
