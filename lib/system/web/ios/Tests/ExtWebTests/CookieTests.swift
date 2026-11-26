import ExtWeb
import XCTest

final class CookieFixtureTests: XCTestCase {
    func testSite() throws {
        struct Case: Decodable { var name: String; var input: String; var valid: Bool; var expect: String? }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load("cookie/v2/site.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            do {
                let site = try parseSite(c.input)
                XCTAssertTrue(c.valid, "\(c.name): accepted")
                XCTAssertEqual(site.string, c.expect, c.name)
            } catch {
                XCTAssertFalse(c.valid, "\(c.name): refused: \(error)")
            }
        }
    }

    func testDomainAndPathMatching() throws {
        struct Domain: Decodable { var name: String; var siteHost: String; var cookieDomain: String; var match: Bool }
        struct Path: Decodable { var name: String; var requestPath: String; var cookiePath: String; var match: Bool }
        struct Domains: Decodable { var cases: [Domain] }
        struct Paths: Decodable { var cases: [Path] }
        let domains: Domains = try Fixtures.load("cookie/v2/domain-match.json")
        let paths: Paths = try Fixtures.load("cookie/v2/path-match.json")
        XCTAssertFalse(domains.cases.isEmpty || paths.cases.isEmpty)
        for c in domains.cases {
            XCTAssertEqual(CookieRules.domainMatches(siteHost: c.siteHost, cookieDomain: c.cookieDomain), c.match, c.name)
        }
        for c in paths.cases {
            XCTAssertEqual(CookieRules.pathMatches(requestPath: c.requestPath, cookiePath: c.cookiePath), c.match, c.name)
        }
    }

    func testCookie() throws {
        struct Case: Decodable { var name: String; var cookie: Cookie; var valid: Bool }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load("cookie/v2/cookie.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            var valid = true
            do { try CookieRules.validate(c.cookie) } catch { valid = false }
            XCTAssertEqual(valid, c.valid, c.name)
            // An error must not carry the secret value.
            var secret = c.cookie
            secret.value = "s3cr3t-value-0f7a"
            do { try CookieRules.validate(secret) } catch {
                XCTAssertFalse("\(error)".contains(secret.value), "\(c.name): the error leaks the value")
            }
        }
    }

    func testBundle() throws {
        struct Case: Decodable { var name: String; var bundle: CookieBundle; var valid: Bool }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load("cookie/v2/bundle.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            var valid = true
            do { try CookieRules.validate(c.bundle) } catch { valid = false }
            XCTAssertEqual(valid, c.valid, c.name)
        }
    }
}

final class CookieBehaviorTests: XCTestCase {
    private let secret = "s3cr3t-value-0f7a"

    func testDescriptionsNeverPrintTheValue() {
        let cookie = Cookie(name: "sid", value: secret, domain: "example.com", path: "/", partitionKey: "https://top.example")
        let bundle = CookieBundle(source: "adapter", site: "https://example.com/", cookie: cookie)
        var dumped = ""
        dump(bundle, to: &dumped)
        let texts = [
            "\(cookie)", String(reflecting: cookie), "\(bundle)", String(reflecting: bundle), dumped,
            String(describing: [cookie]), String(describing: ["k": cookie]),
        ]
        for text in texts {
            XCTAssertFalse(text.contains(secret), text)
        }
        XCTAssertTrue("\(cookie)".contains("sid"))
        XCTAssertTrue("\(cookie)".contains("<redacted>"))
    }

    func testJSONUsesTheWireNames() throws {
        let cookie = Cookie(
            id: 7, ref: "r", name: "sid", value: "v", domain: ".example.com", path: "/", expiry: 5, secure: true,
            httpOnly: true, sameSitePolicy: "lax", partitionKey: "p", hasCrossSiteAncestor: true, attributes: ["x.y": "z"])
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let json = String(decoding: try encoder.encode(cookie), as: UTF8.self)
        XCTAssertEqual(
            json,
            #"{"attributes":{"x.y":"z"},"domain":".example.com","expiry":5,"has_cross_site_ancestor":true,"http_only":true,"id":7,"name":"sid","partition_key":"p","path":"\/","ref":"r","same_site_policy":"lax","secure":true,"value":"v"}"#
        )
        XCTAssertEqual(try JSONDecoder().decode(Cookie.self, from: Data(json.utf8)), cookie)

        // Members Go writes only when set stay out of a minimal cookie.
        let minimal = Cookie(name: "sid", value: "v", domain: "example.com", path: "/")
        let text = String(decoding: try encoder.encode(minimal), as: UTF8.self)
        XCTAssertEqual(text, #"{"domain":"example.com","expiry":0,"http_only":false,"name":"sid","path":"\/","secure":false,"value":"v"}"#)
    }

    func testMissingMembersAreZero() throws {
        let cookie = try JSONDecoder().decode(Cookie.self, from: Data(#"{"name":"a","attributes":null}"#.utf8))
        XCTAssertEqual(cookie, Cookie(name: "a", value: "", domain: "", path: ""))
    }

    func testSessionAndExpiry() {
        let now = Date(timeIntervalSince1970: 1_000)
        XCTAssertTrue(CookieRules.isActive(Cookie(name: "a", value: "", domain: "d", path: "/", expiry: 0), now: now))
        XCTAssertTrue(CookieRules.isActive(Cookie(name: "a", value: "", domain: "d", path: "/", expiry: 1_001), now: now))
        XCTAssertFalse(CookieRules.isActive(Cookie(name: "a", value: "", domain: "d", path: "/", expiry: 1_000), now: now))
    }

    func testMatchesSite() throws {
        let site = try parseSite("https://a.example.com/x/y?q=1")
        let cookie = Cookie(name: "a", value: "v", domain: ".example.com", path: "/x", secure: true)
        XCTAssertTrue(CookieRules.matches(site, cookie))
        let plain = try parseSite("http://a.example.com/x")
        XCTAssertFalse(CookieRules.matches(plain, cookie), "a secure cookie is not sent over http")
        XCTAssertTrue(CookieRules.matches(nil, cookie))
        XCTAssertFalse(CookieRules.matches(site, Cookie(name: "a", value: "v", domain: ".example.com", path: "/z")))
        XCTAssertTrue(CookieRules.sameListed(cookie, cookie))
        var other = cookie
        other.attributes = ["a.b": "c"]
        XCTAssertFalse(CookieRules.sameListed(cookie, other))
    }

    func testIPv6Domains() throws {
        for domain in ["[::1]", "::1", "fe80::1", "::ffff:1.2.3.4"] {
            var cookie = Cookie(name: "a", value: "v", domain: domain, path: "/")
            // A bracketed text is not an address, and neither is anything with a zone.
            let expected = domain != "[::1]"
            var valid = true
            do { try CookieRules.validate(cookie) } catch { valid = false }
            XCTAssertEqual(valid, expected, domain)
            cookie.domain = domain + "%eth0"
            XCTAssertThrowsError(try CookieRules.validate(cookie), domain + "%eth0")
        }
    }
}

final class SiteTests: XCTestCase {
    func testParts() throws {
        let site = try parseSite("HTTPS://Example.COM:8443/a%20b/c?x=1&y=%41#frag%20ment")
        XCTAssertEqual(site.scheme, "https")
        XCTAssertEqual(site.host, "example.com:8443")
        XCTAssertEqual(site.hostname, "example.com")
        XCTAssertEqual(site.path, "/a b/c")
        XCTAssertEqual(site.escapedPath, "/a%20b/c")
        XCTAssertEqual(site.rawQuery, "x=1&y=%41")
        XCTAssertEqual(site.fragment, "frag ment")
        XCTAssertEqual(site.string, "https://example.com:8443/a%20b/c?x=1&y=%41#frag%20ment")
    }

    func testIPv6Host() throws {
        let site = try parseSite("http://[FE80::1%25en0]:80/")
        XCTAssertEqual(site.hostname, "fe80::1%en0")
        XCTAssertThrowsError(try parseSite("http://[1.2.3.4]/"))
        XCTAssertThrowsError(try parseSite("http://[::1/"))
        XCTAssertThrowsError(try parseSite("http://a[::1]/"))
    }

    func testRefusals() {
        for raw in [
            "https://a.example:b/", "https://a:1:2/", "https://exa mple.com/", "https://a.com/%zz", "https://a.com/\u{7}",
            "https:example.com", "https:/example.com", "//example.com/", "javascript:alert(1)", "https://@example.com/",
            "https://a.com#%zz", "HTTP", "https://%41.com/",
        ] {
            XCTAssertThrowsError(try parseSite(raw), raw)
        }
    }

    func testNonASCIIHost() throws {
        let site = try parseSite("https://B\u{00DC}CHER.example/")
        XCTAssertEqual(site.host, "b\u{00FC}cher.example")
        XCTAssertEqual(site.string, "https://b%C3%BCcher.example/")
        let encoded = try parseSite("https://b%C3%BCcher.example/")
        XCTAssertEqual(encoded.host, "b\u{00FC}cher.example")
    }
}
