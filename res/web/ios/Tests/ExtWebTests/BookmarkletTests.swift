import ExtWeb
import XCTest

private let dir = "bookmarklet/v1alpha1/"

final class BookmarkletFixtureTests: XCTestCase {
    func testEncode() throws {
        struct Case: Decodable { var name: String; var source: String; var url: String }
        struct Reject: Decodable { var name: String; var source: String }
        struct File: Decodable { var cases: [Case]; var rejects: [Reject] }
        let file: File = try Fixtures.load(dir + "encode.json")
        XCTAssertFalse(file.cases.isEmpty || file.rejects.isEmpty)
        for c in file.cases {
            XCTAssertEqual(try Bookmarklet.encode(c.source), c.url, c.name)
        }
        for c in file.rejects {
            XCTAssertThrowsError(try Bookmarklet.encode(c.source), c.name)
        }
    }

    func testDecode() throws {
        struct Case: Decodable { var name: String; var url: String; var source: String }
        struct Reject: Decodable { var name: String; var url: String }
        struct File: Decodable { var cases: [Case]; var rejects: [Reject] }
        let file: File = try Fixtures.load(dir + "decode.json")
        XCTAssertFalse(file.cases.isEmpty || file.rejects.isEmpty)
        for c in file.cases {
            XCTAssertEqual(try Bookmarklet.decode(c.url), c.source, c.name)
        }
        for c in file.rejects {
            XCTAssertThrowsError(try Bookmarklet.decode(c.url), c.name)
        }
    }

    func testInstallPage() throws {
        struct Case: Decodable {
            var name: String
            var pageName: String
            var source: String
            var contains: [String]
            var absent: [String]
        }
        struct Reject: Decodable { var name: String; var pageName: String; var source: String }
        struct File: Decodable { var cases: [Case]; var rejects: [Reject] }
        let file: File = try Fixtures.load(dir + "install-page.json")
        XCTAssertFalse(file.cases.isEmpty || file.rejects.isEmpty)
        for c in file.cases {
            let page = try Bookmarklet.installPage(name: c.pageName, source: c.source)
            for want in c.contains { XCTAssertTrue(page.contains(want), "\(c.name): lacks \(want)") }
            for bad in c.absent { XCTAssertFalse(page.contains(bad), "\(c.name): contains \(bad)") }
        }
        for c in file.rejects {
            XCTAssertThrowsError(try Bookmarklet.installPage(name: c.pageName, source: c.source), c.name)
        }
    }
}

final class BookmarkletBehaviorTests: XCTestCase {
    func testRoundTrip() throws {
        for source in ["alert(1)", "a + b % 2;\n// 100%\n", "x = '%41%zz';", "\u{65E5}\u{672C}\u{8A9E} \u{1F600}", "  padded  "] {
            XCTAssertEqual(try Bookmarklet.decode(try Bookmarklet.encode(source)), source)
        }
    }

    func testPercentAtTheEndIsKept() throws {
        // A "%41" needs two characters after the percent sign, so a lone "%4" stays literal.
        XCTAssertEqual(try Bookmarklet.decode("javascript:a%4"), "a%4")
        XCTAssertEqual(try Bookmarklet.decode("javascript:a%41"), "aA")
    }

    func testWrapperSharingItsNewline() throws {
        let url = "javascript:" + "void (function(){\n}).call(window);".addingPercentEncoding(withAllowedCharacters: .alphanumerics)!
        XCTAssertEqual(try Bookmarklet.decode(url), "}).call(window);")
    }

    func testPrefixFoldsLikeGo() {
        // U+0130 lower-cases to "i" in Go, so this is a javascript: URL after TrimSpace(ToLower).
        XCTAssertThrowsError(try Bookmarklet.encode("JAVASCR\u{0130}PT:alert(1)"))
        XCTAssertThrowsError(try Bookmarklet.decode("data:text/html,x"))
        XCTAssertNoThrow(try Bookmarklet.decode("JavaScript:alert(1)"))
    }
}
