import XCTest
import ExtWeb

final class CookieQueryTests: XCTestCase {
    private func cookie(_ value: String = "secret") -> Cookie { Cookie(name: "sid", value: value, domain: "example.test", path: "/") }
    func testSharedFixtures() async throws {
        struct Source: Decodable { let label: String, cookies: [Cookie], warnings: [String], storePath: String }
        struct Expected: Decodable { let value: String, source: String }
        struct Vector: Decodable {
            let name: String, url: String, mode: String, origins: [String], names: [String]
            let inlineOnly: Bool, includeExpired: Bool, allowAllHosts: Bool
            let inline: [Cookie], fallback: [Cookie], sources: [Source], expected: [Expected], calls: [String]
        }
        struct Vectors: Decodable { let cases: [Vector] }
        for v in try Fixtures.load("cookie/query.json", as: Vectors.self).cases {
            var o = CookieQuery.Options(); o.url = v.url; o.mode = v.mode; o.origins = v.origins; o.names = v.names
            o.inlineOnly = v.inlineOnly; o.includeExpired = v.includeExpired; o.allowAllHosts = v.allowAllHosts
            o.inline = try JSONEncoder().encode(v.inline); o.fallbackInline = try JSONEncoder().encode(v.fallback)
            var calls: [String] = []
            let result = try await CookieQuery.get(o) { _, _ in
                .init(sources: v.sources.map { s in .init(adapter: "test", profile: "work", label: s.label) { _, request in
                    XCTAssertEqual(request.names, v.names); XCTAssertEqual(request.includeExpired, v.includeExpired)
                    calls.append(s.label); return .init(cookies: s.cookies, warnings: s.warnings, storePath: s.storePath)
                } })
            }
            XCTAssertEqual(result.cookies.map { $0.cookie.value }, v.expected.map { $0.value }, v.name)
            XCTAssertEqual(result.cookies.map { $0.source }, v.expected.map { $0.source }, v.name)
            XCTAssertEqual(calls, v.calls, v.name)
            for entry in result.cookies {
                XCTAssertEqual(entry.sourceInfo.inline, entry.source.hasPrefix("inline"))
                XCTAssertEqual(entry.sourceInfo.fallback, entry.source == "inline:fallback")
                if !entry.sourceInfo.inline { XCTAssertEqual(entry.sourceInfo.storePath, "/private/store") }
            }
        }
    }
    func testPartialFailuresAndLegacyBinding() async throws {
        var o = CookieQuery.Options(); o.url = "https://example.test"
        struct SecretError: Error {}
        let good = cookie(), wrong = Cookie(name: "other", value: "leak", domain: "example.test", path: "/")
        let result = try await CookieQuery.get(o) { _, _ in
            .init(sources: [
                .init(adapter: "test", profile: "work", label: "bad") { _, _ in throw SecretError() },
                .init(adapter: "test", profile: "work", label: "legacy", list: { _, _ in [good] }, export: { _, _, _ in wrong }),
                .init(adapter: "test", profile: "work", label: "good") { _, _ in .init(cookies: [good], warnings: ["partial"]) }
            ], warnings: ["discovery"])
        }
        XCTAssertEqual(result.cookies.count, 1); XCTAssertEqual(result.cookies.first?.source, "good")
        XCTAssertEqual(result.warnings.count, 4)
        XCTAssertFalse(String(describing: result).contains("secret"))
        XCTAssertFalse(String(describing: result.cookies[0]).contains("secret"))
    }
    func testCancellationTimeoutAndValidation() async throws {
        var o = CookieQuery.Options(); o.url = "https://example.test"; o.timeout = 0.1
        o.inline = try JSONEncoder().encode([cookie()])
        do {
            _ = try await CookieQuery.get(o) { _, _ in
                try await Task.sleep(nanoseconds: 150_000_000); return .init(sources: [])
            }; XCTFail("timeout accepted")
        } catch let e as CookieQuery.Failure { XCTAssertEqual(e.kind, .timeout); XCTAssertEqual(e.partial.cookies.count, 1) }
        o.timeout = 0
        let cancelled = Task { try await CookieQuery.get(o) { _, _ in
            try await Task.sleep(nanoseconds: 1_000_000_000); return .init(sources: [])
        } }
        cancelled.cancel()
        do { _ = try await cancelled.value; XCTFail("cancellation accepted") }
        catch let e as CookieQuery.Failure { XCTAssertEqual(e.kind, .cancelled) }
        o.mode = "first"; o.fallbackInline = Data("invalid".utf8)
        do { _ = try await CookieQuery.get(o); XCTFail("invalid unused fallback accepted") } catch { }
        o.fallbackInline = nil; o.sources = ["a:work"]; o.browsers = ["a"]
        do { _ = try await CookieQuery.get(o); XCTFail("ambiguous selection accepted") } catch { }
    }
    func testInvalidSourcesAndRows() async throws {
        var o = CookieQuery.Options(); o.url = "https://example.test"
        let good = cookie()
        let invalid = Cookie(name: "bad name", value: "secret", domain: "example.test", path: "/")
        let result = try await CookieQuery.get(o) { _, _ in
            .init(sources: [.init(adapter: "test", profile: "work", label: "rows") { _, _ in .init(cookies: [invalid, good], warnings: ["notice"]) }])
        }
        XCTAssertEqual(result.cookies.count, 1); XCTAssertEqual(result.warnings.count, 2)
        XCTAssertEqual(result.warnings.first, "rows: notice")
        o.inline = try JSONEncoder().encode([good])
        do {
            _ = try await CookieQuery.get(o) { _, _ in .init(sources: [.init(adapter: "", profile: "work", label: "invalid") { _, _ in .init(cookies: []) }]) }
            XCTFail("invalid descriptor accepted")
        } catch let error as CookieQuery.Failure { XCTAssertEqual(error.kind, .invalidSource); XCTAssertEqual(error.partial.cookies.count, 1) }
    }
    func testHostSelectionAndLegacySuccess() async throws {
        var o = CookieQuery.Options(); o.url = "https://example.test"; o.preferredSource = "test:work"; o.profiles = ["test": "work"]
        let good = cookie()
        let result = try await CookieQuery.get(o) { _, received in
            XCTAssertEqual(received.preferredSource, o.preferredSource); XCTAssertEqual(received.profiles, o.profiles)
            return .init(sources: [.init(adapter: "test", profile: "work", label: "test:work",
                list: { _, _ in [good] }, export: { _, _, row in row })])
        }
        XCTAssertEqual(result.cookies.count, 1)
        o.url = ""; o.allowAllHosts = true
        let unsupported = try await CookieQuery.get(o) { _, _ in .init(sources: [.init(adapter: "test", profile: "work", label: "legacy",
            list: { _, _ in XCTFail("legacy all-host called"); return [] }, export: { _, _, row in row })]) }
        XCTAssertEqual(unsupported.warnings.count, 1); XCTAssertTrue(unsupported.cookies.isEmpty)
    }
}
