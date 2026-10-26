import ExtWeb
import XCTest

private let dir = "userscript/v1alpha1/"

private struct FixtureRecord: Decodable {
    var target: String
    var id: String
    var name: String
    var revision: String?
    var matches: [String]?
    var excludeMatches: [String]?
    var source: String

    var record: UserscriptRecord {
        UserscriptRecord(
            target: target, id: id, name: name, revision: revision ?? "", matches: matches, excludeMatches: excludeMatches,
            source: source)
    }
}

private func signed(_ record: UserscriptRecord) -> UserscriptRecord {
    var record = record
    record.revision = Userscript.revision(of: record)
    return record
}

private func baseRecord() -> UserscriptRecord {
    UserscriptRecord(
        target: "session", id: "demo", name: "demo", matches: ["https://example.com/*"], excludeMatches: [],
        source: "console.log(1);\n")
}

private func code(_ body: () throws -> Void) -> String {
    do {
        try body()
        return ""
    } catch let error as UserscriptError {
        return error.code
    } catch {
        return "foreign: \(error)"
    }
}

final class UserscriptFixtureTests: XCTestCase {
    func testMatchPatterns() throws {
        struct Case: Decodable { var name: String; var pattern: String; var valid: Bool; var error: String? }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load(dir + "match-patterns.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            var record = baseRecord()
            record.matches = [c.pattern]
            XCTAssertEqual(code { try Userscript.validate(signed(record)) }, c.error ?? "", c.name)
            XCTAssertEqual(c.valid, (c.error ?? "").isEmpty, "\(c.name): fixture inconsistent")
        }
    }

    func testDirectives() throws {
        struct Case: Decodable {
            var name: String
            var source: String
            var matches: [String]?
            var excludeMatches: [String]?
            var error: String?
        }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load(dir + "directives.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            var record = baseRecord()
            record.source = c.source
            record.matches = c.matches
            record.excludeMatches = c.excludeMatches
            XCTAssertEqual(code { try Userscript.validate(signed(record)) }, c.error ?? "", c.name)
        }
    }

    func testMetadata() throws {
        struct Expect: Decodable { var name: String; var id: String; var matches: [String]?; var excludeMatches: [String]? }
        struct Case: Decodable { var name: String; var sourceFile: String; var source: String; var expect: Expect }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load(dir + "metadata.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            let got = Userscript.inferMetadata(
                UserscriptRecord(target: "", id: "", name: "", source: c.source), sourceFile: c.sourceFile)
            XCTAssertEqual(got.name, c.expect.name, c.name)
            XCTAssertEqual(got.id, c.expect.id, c.name)
            XCTAssertEqual(got.matches, c.expect.matches, c.name)
            XCTAssertEqual(got.excludeMatches, c.expect.excludeMatches, c.name)
        }
    }

    func testCompatibility() throws {
        struct Case: Decodable { var name: String; var source: String; var expect: UserscriptCompatibilityReport }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load(dir + "compatibility.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            let got = Userscript.compatibility(of: c.source)
            XCTAssertEqual(got, c.expect, c.name)
            XCTAssertEqual(got.runnable, got.blockers.isEmpty, c.name)
        }
    }

    func testRevision() throws {
        struct Case: Decodable { var name: String; var record: FixtureRecord; var revision: String }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load(dir + "revision.json")
        XCTAssertGreaterThanOrEqual(file.cases.count, 4)
        var seen: [String: String] = [:]
        for c in file.cases {
            XCTAssertEqual(Userscript.revision(of: c.record.record), c.revision, c.name)
            XCTAssertNil(seen[c.revision], "vectors \(seen[c.revision] ?? "") and \(c.name) share a digest")
            seen[c.revision] = c.name
        }
    }

    func testRecords() throws {
        struct Case: Decodable { var name: String; var record: FixtureRecord; var error: String? }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load(dir + "record.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            var record = c.record.record
            if record.revision == "auto" { record.revision = Userscript.revision(of: record) }
            XCTAssertEqual(code { try Userscript.validate(record) }, c.error ?? "", c.name)
        }
    }

    func testReplay() throws {
        struct Case: Decodable {
            var name: String
            var id: String
            var revision: String
            var source: String
            var matches: [String]?
            var excludeMatches: [String]?
            var expect: String?
            var error: String?
        }
        struct File: Decodable { var cases: [Case] }
        let file: File = try Fixtures.load(dir + "replay.json")
        XCTAssertFalse(file.cases.isEmpty)
        for c in file.cases {
            var script = ""
            let got = code {
                script = try Userscript.replaySource(
                    id: c.id, revision: c.revision, source: c.source, matches: c.matches, excludes: c.excludeMatches)
            }
            if let error = c.error {
                XCTAssertEqual(got, error == "-" ? "" : error, c.name)
                XCTAssertEqual(script, "", "\(c.name): produced a script")
            } else {
                XCTAssertEqual(got, "", c.name)
                XCTAssertEqual(script, c.expect, c.name)
            }
        }
    }
}

final class UserscriptBehaviorTests: XCTestCase {
    func testRecordRoundTripKeepsNullApart() throws {
        let nilExcludes = UserscriptRecord(target: "t", id: "i", name: "n", matches: ["https://a/*"], source: "s")
        var emptyExcludes = nilExcludes
        emptyExcludes.excludeMatches = []
        let a = String(decoding: try JSONEncoder().encode(nilExcludes), as: UTF8.self)
        let b = String(decoding: try JSONEncoder().encode(emptyExcludes), as: UTF8.self)
        XCTAssertTrue(a.contains(#""excludeMatches":null"#), a)
        XCTAssertTrue(b.contains(#""excludeMatches":[]"#), b)
        XCTAssertEqual(try JSONDecoder().decode(UserscriptRecord.self, from: Data(a.utf8)), nilExcludes)
        XCTAssertEqual(try JSONDecoder().decode(UserscriptRecord.self, from: Data(b.utf8)), emptyExcludes)
        XCTAssertNotEqual(Userscript.revision(of: nilExcludes), Userscript.revision(of: emptyExcludes))
    }

    func testInferredRecordValidates() throws {
        let source = "// ==UserScript==\n// @name Hello World\n// @match https://a.example/*\n// ==/UserScript==\nconsole.log(1);\n"
        var record = Userscript.inferMetadata(UserscriptRecord(target: "t", id: "", name: "", source: source))
        XCTAssertEqual(record.id, "hello-world")
        record.revision = Userscript.revision(of: record)
        try Userscript.validate(record)
        record.matches = ["https://other.example/*"]
        record.revision = Userscript.revision(of: record)
        XCTAssertEqual(code { try Userscript.validate(record) }, "match_metadata_mismatch")
    }

    func testIDFoldsLikeGo() {
        // Go's ToLower maps U+0130 to "i" and the Kelvin sign to "k"; Swift's full mapping would not.
        let record = Userscript.inferMetadata(UserscriptRecord(target: "t", id: "", name: "\u{0130}\u{212A}-X  y.", source: ""))
        XCTAssertEqual(record.id, "ik-x-y")
    }

    func testLineEndingsAreNotGraphemes() {
        // "\r\n" is one Swift Character, but Go splits on the "\n" alone.
        var record = baseRecord()
        record.source = "// @grant GM_x\r\nconsole.log(1);\r\n"
        XCTAssertEqual(code { try Userscript.validate(signed(record)) }, "unsupported_grant")
    }
}
