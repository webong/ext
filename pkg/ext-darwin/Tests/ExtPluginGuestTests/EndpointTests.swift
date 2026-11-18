import ExtPluginGuest
import XCTest

final class EndpointTests: XCTestCase {
    private func data(_ text: String) -> Data { Data(text.utf8) }

    private func outcome(_ body: () throws -> Void) -> String {
        do {
            try body()
            return "ok"
        } catch EndpointError.invalid {
            return "invalid"
        } catch EndpointError.denied {
            return "denied"
        } catch EndpointError.expired {
            return "expired"
        } catch {
            return "error \(error)"
        }
    }

    /// The end of the JSON value that starts at `start`.
    private func valueEnd(_ b: [UInt8], _ start: Int) -> Int {
        var i = start
        var depth = 0
        var quoted = false
        while i < b.count {
            let c = b[i]
            if quoted {
                if c == UInt8(ascii: "\\") {
                    i += 1
                } else if c == UInt8(ascii: "\"") {
                    quoted = false
                    if depth == 0 { return i + 1 }
                }
            } else {
                switch c {
                case UInt8(ascii: "\""): quoted = true
                case UInt8(ascii: "{"), UInt8(ascii: "["): depth += 1
                case UInt8(ascii: "}"), UInt8(ascii: "]"):
                    if depth == 0 { return i }
                    depth -= 1
                    if depth == 0 { return i + 1 }
                case UInt8(ascii: ","):
                    if depth == 0 { return i }
                default: break
                }
            }
            i += 1
        }
        return i
    }

    /// The raw JSON text of a top-level member of the object `line`. The fixture's
    /// cases include duplicate keys, which a JSON parser would collapse.
    private func member(_ line: [UInt8], _ key: String) -> [UInt8] {
        var i = line.firstIndex(of: UInt8(ascii: "{"))! + 1
        while i < line.count {
            while i < line.count, line[i] == UInt8(ascii: " ") || line[i] == UInt8(ascii: ",") { i += 1 }
            guard i < line.count, line[i] == UInt8(ascii: "\"") else { break }
            let nameEnd = line[(i + 1)...].firstIndex(of: UInt8(ascii: "\""))!
            let name = String(decoding: line[(i + 1)..<nameEnd], as: UTF8.self)
            i = nameEnd + 1
            while i < line.count, line[i] == UInt8(ascii: " ") || line[i] == UInt8(ascii: ":") { i += 1 }
            let stop = valueEnd(line, i)
            if name == key { return Array(line[i..<stop]) }
            i = stop
        }
        return []
    }

    func testEveryOutcomeOfTheSharedFixtureIsReproduced() throws {
        let fixture = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("lib/plugin/testdata/v1/endpoint-cases.json")
        let text = try String(contentsOf: fixture, encoding: .utf8)
        var total = 0
        var seen = Set<String>()
        for raw in text.split(separator: "\n") {
            var line = Array(raw.utf8)
            while let last = line.last, last == UInt8(ascii: ",") || last == UInt8(ascii: " ") || last == UInt8(ascii: "\r") { line.removeLast() }
            while let first = line.first, first == UInt8(ascii: " ") { line.removeFirst() }
            guard line.starts(with: Array(#"{"name""#.utf8)) else { continue }
            let name = String(decoding: member(line, "name"), as: UTF8.self)
            let endpoint = Data(member(line, "endpoint"))
            let policy = Data(member(line, "policy"))
            let wantCheck = String(decoding: member(line, "check"), as: UTF8.self).trimmingCharacters(in: CharacterSet(charactersIn: "\""))
            let wantValidate = String(decoding: member(line, "validate"), as: UTF8.self).trimmingCharacters(in: CharacterSet(charactersIn: "\""))
            XCTAssertEqual(outcome { try Endpoint.check(endpoint) }, wantCheck, "check: \(name)")
            XCTAssertEqual(outcome { try Endpoint.validate(endpoint, policy: policy) }, wantValidate, "validate: \(name)")
            seen.insert(wantValidate)
            total += 1
        }
        XCTAssertEqual(total, 213)
        XCTAssertEqual(seen, ["ok", "invalid", "denied", "expired"])
    }

    func testInputsThatAreNotEndpoints() {
        let endpoint = data(#"{"scheme":"ws","address":"localhost:80","expiresAt":"2030-01-01T00:30:00Z"}"#)
        XCTAssertEqual(outcome { try Endpoint.check(endpoint) }, "ok")
        XCTAssertEqual(outcome { try Endpoint.check(self.data("[]")) }, "invalid")
        XCTAssertEqual(outcome { try Endpoint.check(Data()) }, "invalid")
        // A policy that names no scheme admits nothing.
        XCTAssertEqual(outcome { try Endpoint.validate(endpoint, policy: self.data(#"{"now":"2030-01-01T00:00:00Z"}"#)) }, "denied")
        XCTAssertEqual(outcome { try Endpoint.validate(endpoint, policy: self.data(#"{"schemes":["ws"],"now":"2030-01-01T00:00:00Z"}"#)) }, "ok")
        XCTAssertEqual(outcome { try Endpoint.validate(endpoint, policy: self.data(#"{"schemes":["ws"],"now":"2030-01-01T01:00:00Z"}"#)) }, "expired")
    }
}
