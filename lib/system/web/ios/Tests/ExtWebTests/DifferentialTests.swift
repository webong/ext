import ExtWeb
import XCTest

// Compares the port with a corpus the Go implementation produced (EXT_WEB_DIFF). Skipped without it.
final class DifferentialTests: XCTestCase {
    struct SiteCase: Decodable {
        var input: String; var ok: Bool; var string: String?; var hostname: String?; var host: String?
        var path: String?; var escapedPath: String?; var rawQuery: String?; var forceQuery: Bool?; var fragment: String?
    }
    struct DomainCase: Decodable { var domain: String; var valid: Bool }
    struct IDCase: Decodable { var name: String; var id: String; var outName: String }
    struct BMCase: Decodable {
        var input: String; var encodeOK: Bool; var url: String?; var decodeOK: Bool; var source: String?
    }
    struct Corpus: Decodable { var sites: [SiteCase]; var domains: [DomainCase]; var ids: [IDCase]; var bookmarklets: [BMCase] }

    private func corpus() throws -> Corpus {
        guard let path = ProcessInfo.processInfo.environment["EXT_WEB_DIFF"] else { throw XCTSkip("EXT_WEB_DIFF is not set") }
        return try JSONDecoder().decode(Corpus.self, from: Data(contentsOf: URL(fileURLWithPath: path)))
    }

    private func report(_ label: String, _ mismatches: [String], of total: Int) {
        print("\(label): \(total - mismatches.count)/\(total) agree")
        for line in mismatches.prefix(25) { print("  MISMATCH \(line)") }
        XCTAssertTrue(mismatches.isEmpty, "\(label): \(mismatches.count) of \(total) differ")
    }

    func testSites() throws {
        let corpus = try corpus()
        var bad: [String] = []
        for c in corpus.sites {
            let site = try? parseSite(c.input)
            if (site != nil) != c.ok { bad.append("\(c.input.debugDescription): ok go=\(c.ok) swift=\(site != nil)"); continue }
            guard let site else { continue }
            let got = [site.string, site.hostname, site.host, site.path, site.escapedPath, site.rawQuery, site.forceQuery ? "T" : "F", site.fragment]
            let want = [c.string ?? "", c.hostname ?? "", c.host ?? "", c.path ?? "", c.escapedPath ?? "", c.rawQuery ?? "", (c.forceQuery ?? false) ? "T" : "F", c.fragment ?? ""]
            if got != want { bad.append("\(c.input.debugDescription): swift=\(got) go=\(want)") }
        }
        report("sites", bad, of: corpus.sites.count)
    }

    func testDomains() throws {
        let corpus = try corpus()
        var bad: [String] = []
        for c in corpus.domains {
            var valid = true
            do { try CookieRules.validate(Cookie(name: "a", value: "v", domain: c.domain, path: "/")) } catch { valid = false }
            if valid != c.valid { bad.append("\(c.domain.debugDescription): go=\(c.valid) swift=\(valid)") }
        }
        report("domains", bad, of: corpus.domains.count)
    }

    func testIDs() throws {
        let corpus = try corpus()
        var bad: [String] = []
        for c in corpus.ids {
            let record: UserscriptRecord
            if c.name.hasPrefix("file:") {
                record = Userscript.inferMetadata(UserscriptRecord(target: "", id: "", name: "", source: ""), sourceFile: String(c.name.dropFirst(5)))
            } else if c.name.hasPrefix("direct:") {
                record = Userscript.inferMetadata(UserscriptRecord(target: "", id: "", name: String(c.name.dropFirst(7)), source: ""))
            } else {
                record = Userscript.inferMetadata(UserscriptRecord(target: "", id: "", name: "", source: "// ==UserScript==\n// @name \(c.name)\n// ==/UserScript==\n"))
            }
            if record.id != c.id || record.name != c.outName { bad.append("\(c.name.debugDescription): go=(\(c.id),\(c.outName)) swift=(\(record.id),\(record.name))") }
        }
        report("ids", bad, of: corpus.ids.count)
    }

    func testBookmarklets() throws {
        let corpus = try corpus()
        var bad: [String] = []
        for c in corpus.bookmarklets {
            let url = try? Bookmarklet.encode(c.input)
            if (url != nil) != c.encodeOK || url != c.url { bad.append("encode \(c.input.debugDescription): go=\(String(describing: c.url)) swift=\(String(describing: url))") }
            let source = try? Bookmarklet.decode(c.input)
            if (source != nil) != c.decodeOK || source != c.source { bad.append("decode \(c.input.debugDescription): go=\(String(describing: c.source)) swift=\(String(describing: source))") }
        }
        report("bookmarklets", bad, of: corpus.bookmarklets.count)
    }
}
