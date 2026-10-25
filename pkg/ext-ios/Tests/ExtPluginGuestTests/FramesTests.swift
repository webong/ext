@testable import ExtPluginGuest
import Foundation
import XCTest

/// The shared fixtures for progress events and reverse calls (lib/plugin/testdata/v1), which the Go
/// contract computes and every SDK reproduces: frames, the sender's rate limiter and the wire model.
final class FramesFixtureTests: XCTestCase {
    private static let directory: URL = {
        if let path = ProcessInfo.processInfo.environment["EXT_FIXTURES"] { return URL(fileURLWithPath: path) }
        // pkg/ext-ios/Tests/ExtPluginGuestTests/FramesTests.swift -> the repository root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("lib/plugin/testdata/v1")
    }()

    private func file(_ name: String) throws -> Data {
        try Data(contentsOf: Self.directory.appendingPathComponent(name))
    }

    private func fixture(_ name: String) throws -> [String: JSONValue] {
        try XCTUnwrap(JSONValue.parse(Array(file(name))).object)
    }

    private func cases(_ value: JSONValue?) throws -> [[String: JSONValue]] {
        guard case .array(let items)? = value else { throw XCTSkip("not a list") }
        return items.compactMap { $0.object }
    }

    private func string(_ c: [String: JSONValue], _ key: String) -> String { c[key]?.string ?? "" }

    private func number(_ c: [String: JSONValue], _ key: String) -> Int64 {
        if case .number(let text)? = c[key] { return Int64(text) ?? 0 }
        return 0
    }

    private func flag(_ c: [String: JSONValue], _ key: String) -> Bool { c[key] == .bool(true) }

    /// Collects the first few mismatches so a failure says what diverged.
    private struct Mismatches {
        var first: [String] = []
        var count = 0
        mutating func note(_ section: String, _ name: String, _ what: String) {
            count += 1
            if first.count < 12 { first.append("\(section): \(name): \(what)") }
        }
    }

    func testFrames() throws {
        let root = try fixture("frame-cases.json")
        var bad = Mismatches()
        var total = 0
        let sections: [(String, (JSONValue, String) throws -> Void)] = [
            ("progress", { value, _ in _ = try Frames.progressFrame(value) }),
            ("reverseRequest", { value, _ in try Frames.reverseRequest(value) }),
            ("reverseResponse", { value, id in _ = try Frames.reverseResponse(value, id: id) }),
        ]
        for (name, judge) in sections {
            let list = try cases(root[name])
            let valid = list.filter { flag($0, "valid") }.count
            XCTAssertTrue(list.count >= 20 && valid >= 5 && list.count - valid >= 5, "\(name) proves little")
            for c in list {
                total += 1
                let id = string(c, "id")
                let frame = Array(string(c, "frame").utf8)
                var accepted = true
                do { try judge(Frames.decode(frame), id) } catch { accepted = false }
                if accepted != flag(c, "valid") { bad.note(name, string(c, "name"), "accepted \(accepted): \(string(c, "frame"))") }
            }
        }
        XCTAssertGreaterThanOrEqual(total, 500, "only \(total) frame cases")
        XCTAssertEqual(bad.count, 0, bad.first.joined(separator: "\n"))
    }

    func testLimiter() throws {
        let list = try cases(try fixture("progress-limiter-cases.json")["cases"])
        XCTAssertGreaterThanOrEqual(list.count, 10)
        var bad = Mismatches()
        for c in list {
            var limiter = Frames.Limiter(eventsPerSecond: number(c, "eventsPerSecond"), bytesPerSecond: number(c, "bytesPerSecond"), now: 0)
            for (index, step) in try cases(c["steps"]).enumerated() {
                let got = limiter.admit(textBytes: Int(number(step, "textBytes")), now: UInt64(number(step, "atMs")) * 1_000_000)
                let want = flag(step, "admitted")
                if (got != nil) != want || (want && got != UInt64(number(step, "dropped"))) {
                    bad.note("limiter", string(c, "name"), "step \(index): \(String(describing: got)), want \(want) \(number(step, "dropped"))")
                }
            }
            if limiter.pending() != UInt64(number(c, "pending")) { bad.note("limiter", string(c, "name"), "pending count") }
        }
        XCTAssertEqual(bad.count, 0, bad.first.joined(separator: "\n"))
    }

    func testModel() throws {
        let bytes = Array(try file("model-cases.json"))
        let root = try XCTUnwrap(JSONValue.parse(bytes).object)
        var bad = Mismatches()
        let descriptors = try cases(root["descriptors"])
        XCTAssertGreaterThan(descriptors.count, 100)
        for c in descriptors {
            let got = (try? Guest(descriptorJSON: Data(string(c, "descriptor").utf8)) { _ in Data() }) != nil
            if got != flag(c, "valid") { bad.note("descriptor", string(c, "name"), "accepted \(got): \(string(c, "descriptor"))") }
        }
        // The descriptor the requests are judged against.
        let selected = try XCTUnwrap(rawMember(in: bytes, key: "requestDescriptor"))
        let requests = try cases(root["requests"])
        XCTAssertGreaterThan(requests.count, 100)
        let null = Int32(open("/dev/null", O_WRONLY))
        defer { close(null) }
        for c in requests {
            final class Seen: @unchecked Sendable {
                var ran = false
                var limits: ProgressLimits?
            }
            let seen = Seen()
            let guest = try Guest(descriptorJSON: Data(selected)) { call in
                seen.ran = true
                seen.limits = call.progressLimits
                return Data()
            }
            let connection = Connection(declared: guest.declared, output: null)
            // A request the engine will not take at all just does not run.
            _ = try? guest.invoke(Data(string(c, "request").utf8), timeoutMilliseconds: 5000, connection: connection)
            if seen.ran != flag(c, "valid") {
                bad.note("request", string(c, "name"), "accepted \(seen.ran): \(string(c, "request"))")
                continue
            }
            if !seen.ran { continue }
            // The call gets a reporter when its operation declared progress and the request carries
            // a rate; the rate has its defaults filled in.
            if (seen.limits != nil) != flag(c, "reporter") {
                bad.note("request", string(c, "name"), "reporter \(seen.limits != nil)")
            } else if let limits = seen.limits,
                limits.eventsPerSecond != number(c, "events") || limits.bytesPerSecond != number(c, "bytes")
            {
                bad.note("request", string(c, "name"), "rate \(limits)")
            }
        }
        XCTAssertEqual(bad.count, 0, bad.first.joined(separator: "\n"))
    }
}

final class FramesUnitTests: XCTestCase {
    func testDescriptorWireShape() throws {
        let descriptor = Descriptor(
            identity: Identity(id: "a/b", revision: "1"),
            contracts: [Contract(name: "c.d", version: "v1", operations: [.reporting("long"), Operation(name: "quick")])],
            reverse: [Contract(name: "host.svc", version: "v1", operations: [Operation(name: "sign", surface: "signing")])])
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        let text = String(decoding: try encoder.encode(descriptor), as: UTF8.self)
        XCTAssertEqual(
            text,
            #"{"apiVersion":"ext.plugin\/v1","contracts":[{"name":"c.d","operations":[{"name":"long","progress":true},{"name":"quick"}],"version":"v1"}],"identity":{"id":"a\/b","revision":"1"},"reverse":[{"name":"host.svc","operations":[{"name":"sign","surface":"signing"}],"version":"v1"}]}"#
                .replacingOccurrences(of: "\\/", with: "/"))
        XCTAssertEqual(try JSONDecoder().decode(Descriptor.self, from: Data(text.utf8)), descriptor)
        // Neither member is written when unset.
        let plain = Descriptor(identity: Identity(id: "a/b", revision: "1"), contracts: [])
        XCTAssertFalse(String(decoding: try encoder.encode(plain), as: UTF8.self).contains("reverse"))
    }

    func testSplitCutsOnCharacters() {
        let text = String(repeating: "x", count: 2047) + "\u{1F600}" + "é"
        let pieces = ProgressEvent.split(text)
        XCTAssertEqual(pieces.count, 2)
        XCTAssertEqual(pieces[0], String(repeating: "x", count: 2047))
        XCTAssertEqual(pieces[1], "\u{1F600}é")
        XCTAssertEqual(ProgressEvent.split("").count, 0)
        XCTAssertTrue(ProgressEvent.split(String(repeating: "é", count: 5000)).allSatisfy { $0.utf8.count <= 2048 })
    }

    func testInstants() throws {
        XCTAssertEqual(try Frames.instant("2026-10-24T14:00:00Z"), 1_792_850_400_000)
        XCTAssertEqual(try Frames.instant("2026-10-24T16:00:00.120+02:00"), 1_792_850_400_120)
        XCTAssertEqual(Frames.format(milliseconds: 1_792_850_400_120), "2026-10-24T14:00:00.120Z")
        XCTAssertEqual(Frames.format(milliseconds: 1_792_850_400_000), "2026-10-24T14:00:00Z")
        XCTAssertEqual(Frames.format(milliseconds: -1), "1969-12-31T23:59:59.999Z")
        for bad in ["0001-01-01T00:00:00Z", "2026-02-30T00:00:00Z", "2026-10-24 14:00:00Z", "2026-10-24T24:00:00Z", "2026-10-24T14:00:00+24:00"] {
            XCTAssertThrowsError(try Frames.instant(bad), bad)
        }
        // Go's zero time, in another zone, is still the zero time.
        XCTAssertThrowsError(try Frames.instant("0001-01-01T01:00:00+01:00"))
        XCTAssertNoThrow(try Frames.instant("0001-01-01T00:00:00.5Z"))
    }

    func testPayloadMemberKeepsNumberText() {
        let call = Call(
            id: "1", plugin: Identity(id: "a", revision: "1"), contract: ContractRef(name: "c", version: "v1"), operation: "o",
            surface: nil, payload: Data(#"{"op":"sign","payload":{"n":1.10,"s":"é"},"x":[1,2]}"#.utf8),
            expiry: DispatchTime.now() + .seconds(5), channel: nil)
        XCTAssertEqual(call.payloadMember("payload").map { String(decoding: $0, as: UTF8.self) }, #"{"n":1.10,"s":"é"}"#)
        XCTAssertEqual(call.payloadMember("x").map { String(decoding: $0, as: UTF8.self) }, "[1,2]")
        XCTAssertNil(call.payloadMember("missing"))
        // Without a frame channel, reporting is unsupported and there is no caller.
        XCTAssertNil(call.reverse)
        XCTAssertNil(call.sessionReverse)
        XCTAssertNil(call.progressLimits)
        XCTAssertThrowsError(try call.progress(.log("x"))) { XCTAssertEqual(($0 as? FrameError)?.code, .unsupported) }
    }
}
