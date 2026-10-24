@testable import ExtPluginGuest
import Foundation
import XCTest

/// The router behind `JSONLine.serve`: progress events, reverse calls and the rules a host that
/// breaks them meets. The host is a test thread on pipes.
final class RouterTests: XCTestCase {
    static let contract = ContractRef(name: "test.frames", version: "v1")
    static let hostService = ContractRef(name: "host.svc", version: "v1")

    static let descriptor = Descriptor(
        identity: Identity(id: "swift/frames", revision: "r1"),
        contracts: [
            Contract(
                name: "test.frames", version: "v1",
                operations: [
                    .reporting("events"), .reporting("flood"), .reporting("invalid"), Operation(name: "quiet"),
                    Operation(name: "ask"), Operation(name: "twice"), Operation(name: "late"), Operation(name: "hold"),
                    Operation(name: "session"), Operation(name: "stale"),
                ])
        ],
        reverse: [Contract(name: "host.svc", version: "v1", operations: [Operation(name: "sign")])])

    private static func quoted(_ text: String) -> String {
        (try? JSONEncoder().encode(text)).map { String(decoding: $0, as: UTF8.self) } ?? "\"\""
    }

    /// How a reverse call came out, as JSON.
    static func attempt(_ caller: ReverseCaller?, _ operation: String, _ payload: Data?, timeout: TimeInterval? = nil) -> String {
        guard let caller else { return "{\"frame\":\"nocaller\"}" }
        do {
            let result = try timeout.map { try caller.call(hostService, operation, payload: payload, timeout: $0) }
                ?? caller.call(hostService, operation, payload: payload)
            return "{\"result\":" + String(decoding: result, as: UTF8.self) + "}"
        } catch let error as RemoteError {
            return "{\"code\":" + quoted(error.code) + "}"
        } catch let error as FrameError {
            return "{\"frame\":" + quoted(error.code.rawValue) + "}"
        } catch {
            return "{\"frame\":\"other\"}"
        }
    }

    /// What the "stale" operation kept of its call.
    final class Kept: @unchecked Sendable {
        var caller: ReverseCaller?
    }
    static let kept = Kept()

    static func handle(_ call: Call) throws -> Data {
        func json(_ text: String) -> Data { Data(text.utf8) }
        switch call.operation {
        case "stale":
            kept.caller = call.reverse
            return Data()
        case "events":
            let first = try call.progress(.log("a")) == .sent
            let second = try call.progress(.fraction(1, of: 2)) == .sent
            return json("{\"sent\":[\(first),\(second)]}")
        case "flood":
            var dropped = 0
            for _ in 0..<300 where try call.progress(.log("x")) == .dropped { dropped += 1 }
            return json("{\"dropped\":\(dropped)}")
        case "quiet":
            do {
                _ = try call.progress(.log("x"))
                return json("{\"unsupported\":false}")
            } catch let error as FrameError {
                return json("{\"unsupported\":\(error.code == .unsupported)}")
            }
        case "invalid":
            var codes: [String] = []
            let events = [
                ProgressEvent(kind: "mystery"), ProgressEvent.fraction(3, of: 2), ProgressEvent.log(String(repeating: "x", count: 2049)),
                ProgressEvent(kind: "log", stream: "both"), ProgressEvent(kind: "progress", stream: "out"),
            ]
            for event in events {
                do {
                    _ = try call.progress(event)
                    codes.append("\"sent\"")
                } catch let error as FrameError {
                    codes.append(quoted(error.code.rawValue))
                }
            }
            return json("{\"codes\":[" + codes.joined(separator: ",") + "]}")
        case "ask":
            struct Input: Decodable { var op: String? }
            let op = try call.decodePayload(Input.self).op ?? "sign"
            return json(attempt(call.reverse, op, call.payloadMember("payload")))
        case "twice":
            let first = OutcomeBox()
            let done = DispatchSemaphore(value: 0)
            let reverse = call.reverse
            Thread {
                first.value = attempt(reverse, "sign", Data("1".utf8))
                done.signal()
            }.start()
            let second = attempt(reverse, "sign", Data("2".utf8))
            done.wait()
            return json("{\"both\":[\(first.value),\(second)]}")
        case "late":
            return json(attempt(call.reverse, "sign", nil, timeout: 0.1))
        case "hold":
            while !call.connectionEnded { Thread.sleep(forTimeInterval: 0.002) }
            return Data()
        case "session":
            let session = call.sessionReverse
            let answer = session == nil ? "null" : attempt(session, "sign", nil)
            return json("{\"session\":\(session != nil),\"reverse\":\(call.reverse != nil),\"answer\":\(answer)}")
        default:
            throw RemoteError(code: "unknown", message: call.operation)
        }
    }

    final class OutcomeBox: @unchecked Sendable { var value = "" }

    /// One direction of a connection: a pipe.
    struct Pipe {
        let read: Int32
        let write: Int32
        init() {
            var fds: [Int32] = [0, 0]
            precondition(pipe(&fds) == 0)
            read = fds[0]
            write = fds[1]
        }
    }

    /// The host: serves a guest on pipes and reads what it writes.
    final class Host: @unchecked Sendable {
        private let toGuest = Pipe()
        private let fromGuest = Pipe()
        private let lock = NSCondition()
        private var frames: [String] = []
        private var finished = false
        private var outcome: Error?
        private var next = 0
        private var closedInput = false

        init(hello: Bool = true) throws {
            let guest = try Guest(descriptor: RouterTests.descriptor, handler: RouterTests.handle)
            let toGuestRead = toGuest.read
            let fromGuestWrite = fromGuest.write
            Thread { [self] in
                do {
                    try JSONLine.serve(guest, input: toGuestRead, output: fromGuestWrite)
                } catch {
                    lock.lock()
                    outcome = error
                    lock.unlock()
                }
                close(fromGuestWrite)
                lock.lock()
                finished = true
                lock.broadcast()
                lock.unlock()
            }.start()
            let fromGuestRead = fromGuest.read
            Thread { [self] in
                let reader = FrameReader(descriptor: fromGuestRead)
                while let frame = try? reader.next() {
                    lock.lock()
                    frames.append(String(decoding: frame, as: UTF8.self))
                    lock.broadcast()
                    lock.unlock()
                }
            }.start()
            if hello { send(Host.hello()) }
        }

        static func hello(_ extra: String? = nil) -> String {
            "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\",\"deadline\":\""
                + Frames.format(milliseconds: Connection.now() + 30_000) + "\"" + (extra.map { "," + $0 } ?? "") + "}"
        }

        func send(_ frame: String) {
            let bytes = Array((frame + "\n").utf8)
            var offset = 0
            while offset < bytes.count {
                let n = bytes.withUnsafeBytes { sysWrite(toGuest.write, $0.baseAddress! + offset, $0.count - offset) }
                if n < 0 { return }
                offset += n
            }
        }

        /// The next frame the guest wrote, or nil when none comes within `seconds`.
        func tryReceive(_ seconds: TimeInterval) -> String? {
            let limit = Date(timeIntervalSinceNow: seconds)
            lock.lock()
            defer { lock.unlock() }
            while frames.isEmpty {
                if !lock.wait(until: limit) && frames.isEmpty { return nil }
            }
            return frames.removeFirst()
        }

        func receive(file: StaticString = #filePath, line: UInt = #line) throws -> [String: Any] {
            let frame = try XCTUnwrap(tryReceive(5), "the guest wrote nothing", file: file, line: line)
            return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(frame.utf8)) as? [String: Any], file: file, line: line)
        }

        /// A request for `operation`; a call whose operation reports progress asks for events.
        @discardableResult
        func request(_ operation: String, _ payload: String, progress: Bool) -> String {
            next += 1
            let id = String(next)
            send(
                "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"\(id)\",\"plugin\":{\"id\":\"swift/frames\",\"revision\":\"r1\"},"
                    + "\"contract\":{\"name\":\"test.frames\",\"version\":\"v1\"},\"operation\":\"\(operation)\","
                    + "\"payload\":\(payload),\"deadline\":\"" + Frames.format(milliseconds: Connection.now() + 30_000) + "\""
                    + (progress ? ",\"progress\":{\"eventsPerSecond\":5}" : "") + "}")
            return id
        }

        /// Reads frames up to the response to `id`: those before it, and it.
        func untilResponse(_ id: String, file: StaticString = #filePath, line: UInt = #line) throws -> (before: [[String: Any]], response: [String: Any]) {
            var before: [[String: Any]] = []
            while true {
                let frame = try receive(file: file, line: line)
                if frame["frame"] == nil, frame["id"] as? String == id { return (before, frame) }
                before.append(frame)
            }
        }

        /// Ends the host's side and waits for the guest to end; what the session threw, or nil.
        func finish() -> Error? {
            lock.lock()
            if !closedInput {
                closedInput = true
                close(toGuest.write)
            }
            let limit = Date(timeIntervalSinceNow: 10)
            while !finished {
                if !lock.wait(until: limit) && !finished { break }
            }
            let done = finished
            let result = outcome
            lock.unlock()
            XCTAssertTrue(done, "the guest did not end")
            return result
        }

        var sessionOutcome: Error? {
            lock.lock()
            defer { lock.unlock() }
            return outcome
        }
    }

    static func started() throws -> Host {
        let host = try Host()
        XCTAssertEqual(try host.receive()["id"] as? String, "hello")
        return host
    }

    static func answer(_ id: String, _ body: String) -> String {
        "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"\(id)\"" + (body.isEmpty ? "" : "," + body) + "}"
    }

    private func payload(_ response: [String: Any]) -> [String: Any] { response["payload"] as? [String: Any] ?? [:] }

    // MARK: tests

    func testHelloServesTheDescriptor() throws {
        let host = try Host()
        let hello = try host.receive()
        let text = String(decoding: try JSONSerialization.data(withJSONObject: hello, options: [.sortedKeys]), as: UTF8.self)
        XCTAssertTrue(text.contains("\"name\":\"events\",\"progress\":true") || text.contains("\"progress\":true,\"name\":\"events\""), text)
        XCTAssertFalse(text.contains("\"name\":\"quiet\",\"progress\""), text)
        XCTAssertTrue(text.contains("\"reverse\":[{"), text)
        XCTAssertNil(host.finish())
    }

    func testEventsReachTheHostInOrderBeforeTheResult() throws {
        let host = try Self.started()
        let id = host.request("events", "null", progress: true)
        let exchange = try host.untilResponse(id)
        XCTAssertEqual(exchange.before.count, 2, "\(exchange.before)")
        for event in exchange.before {
            XCTAssertEqual(event["frame"] as? String, "progress")
            XCTAssertEqual(event["parent"] as? String, id)
        }
        XCTAssertEqual(exchange.before[0]["kind"] as? String, "log")
        XCTAssertEqual(exchange.before[0]["text"] as? String, "a")
        XCTAssertEqual(exchange.before[1]["kind"] as? String, "progress")
        XCTAssertEqual(exchange.before[1]["completed"] as? Int, 1)
        XCTAssertEqual(exchange.before[1]["total"] as? Int, 2)
        XCTAssertEqual(payload(exchange.response)["sent"] as? [Bool], [true, true])
        XCTAssertNil(host.tryReceive(0.1), "a frame after the response")
        XCTAssertNil(host.finish())
    }

    func testFloodIsLimitedAndEveryDropCounted() throws {
        let host = try Self.started()
        let id = host.request("flood", "null", progress: true)
        let exchange = try host.untilResponse(id)
        let dropped = try XCTUnwrap(payload(exchange.response)["dropped"] as? Int)
        let delivered = exchange.before.filter { $0["text"] != nil }.count
        let reported = exchange.before.reduce(0) { $0 + ($1["dropped"] as? Int ?? 0) }
        XCTAssertTrue(delivered >= 10 && delivered + dropped == 300 && reported == dropped, "\(delivered) delivered, \(dropped) dropped, \(reported) reported")
        // The drops still unreported when the handler returned are in a marker with only a kind and a count.
        let last = try XCTUnwrap(exchange.before.last)
        XCTAssertNil(last["text"])
        XCTAssertEqual(last["kind"] as? String, "log")
        XCTAssertGreaterThan(last["dropped"] as? Int ?? 0, 0)
        XCTAssertNil(host.finish())
    }

    func testAnUndeclaredOperationHasNoReporter() throws {
        let host = try Self.started()
        var id = host.request("quiet", "null", progress: false)
        var exchange = try host.untilResponse(id)
        XCTAssertTrue(exchange.before.isEmpty)
        XCTAssertEqual(payload(exchange.response)["unsupported"] as? Bool, true)
        // A rate the host sets for an operation that did not declare progress is not a reporter either.
        id = host.request("quiet", "null", progress: true)
        exchange = try host.untilResponse(id)
        XCTAssertTrue(exchange.before.isEmpty)
        XCTAssertEqual(payload(exchange.response)["unsupported"] as? Bool, true)
        XCTAssertNil(host.finish())
    }

    func testInvalidEventsAreRefusedAndNothingIsSent() throws {
        let host = try Self.started()
        let id = host.request("invalid", "null", progress: true)
        let exchange = try host.untilResponse(id)
        XCTAssertTrue(exchange.before.isEmpty, "\(exchange.before)")
        XCTAssertEqual(payload(exchange.response)["codes"] as? [String], Array(repeating: "invalid", count: 5))
        XCTAssertNil(host.finish())
    }

    func testReverseCallRoundTrip() throws {
        let host = try Self.started()
        var id = host.request("ask", #"{"op":"sign","payload":{"n":1.10}}"#, progress: false)
        var request = try host.receive()
        XCTAssertEqual(request["frame"] as? String, "reverse_request")
        XCTAssertEqual(request["id"] as? String, "g1")
        XCTAssertEqual(request["parent"] as? String, id)
        XCTAssertEqual(request["operation"] as? String, "sign")
        XCTAssertEqual(request["contract"] as? [String: String], ["name": "host.svc", "version": "v1"])
        XCTAssertEqual(request["payload"] as? [String: Double], ["n": 1.10])
        host.send(Self.answer("g1", "\"payload\":\"signed\""))
        XCTAssertEqual(payload(try host.untilResponse(id).response)["result"] as? String, "signed")
        // The host's error comes back with its code, and ids go on counting.
        id = host.request("ask", #"{"op":"sign"}"#, progress: false)
        request = try host.receive()
        XCTAssertEqual(request["id"] as? String, "g2")
        XCTAssertNil(request["payload"])
        host.send(Self.answer("g2", #""error":{"code":"denied","message":"reverse call refused"}"#))
        XCTAssertEqual(payload(try host.untilResponse(id).response)["code"] as? String, "denied")
        // An operation the descriptor does not list is refused without a frame.
        id = host.request("ask", #"{"op":"nothere"}"#, progress: false)
        let exchange = try host.untilResponse(id)
        XCTAssertTrue(exchange.before.isEmpty)
        XCTAssertEqual(payload(exchange.response)["code"] as? String, "reverse_undeclared")
        XCTAssertNil(host.finish())
    }

    func testSeveralReverseCallsMayBeOutstanding() throws {
        let host = try Self.started()
        let id = host.request("twice", "null", progress: false)
        let first = try host.receive()
        let second = try host.receive()
        XCTAssertEqual(Set([first["id"] as? String, second["id"] as? String]), Set(["g1", "g2"]))
        // Answered in the other order, each with the payload of the request it answers.
        for request in [second, first] {
            host.send(Self.answer(request["id"] as! String, "\"payload\":\(request["payload"]!)"))
        }
        let both = payload(try host.untilResponse(id).response)["both"] as? [[String: Int]]
        XCTAssertEqual(Set((both ?? []).compactMap { $0["result"] }), Set([1, 2]))
        XCTAssertNil(host.finish())
    }

    func testSessionScopedCall() throws {
        let host = try Self.started()
        let id = host.request("session", "null", progress: false)
        let request = try host.receive()
        XCTAssertNil(request["parent"])
        host.send(Self.answer(request["id"] as! String, "\"payload\":\"ok\""))
        let result = payload(try host.untilResponse(id).response)
        XCTAssertEqual(result["session"] as? Bool, true)
        XCTAssertEqual(result["reverse"] as? Bool, true)
        XCTAssertEqual((result["answer"] as? [String: Any])?["result"] as? String, "ok")
        XCTAssertNil(host.finish())
    }

    func testACallerKeptPastItsCallWritesNothing() throws {
        let host = try Self.started()
        let id = host.request("stale", "null", progress: false)
        _ = try host.untilResponse(id)
        // A request names the call it belongs to, so a caller kept past the call writes nothing.
        XCTAssertThrowsError(try Self.kept.caller?.call(Self.hostService, "sign", payload: nil)) {
            XCTAssertEqual(($0 as? FrameError)?.code, .closed)
        }
        XCTAssertNil(host.tryReceive(0.1), "a frame for a call that is over")
        XCTAssertNil(host.finish())
    }

    func testAnAnswerToAnAbandonedRequestIsDropped() throws {
        let host = try Self.started()
        let id = host.request("late", "null", progress: false)
        let request = try host.receive()
        XCTAssertEqual(request["id"] as? String, "g1")
        XCTAssertEqual(payload(try host.untilResponse(id).response)["frame"] as? String, "deadline")
        // The late answer is dropped; the session goes on.
        host.send(Self.answer("g1", "\"payload\":1"))
        let next = host.request("events", "null", progress: true)
        XCTAssertEqual(payload(try host.untilResponse(next).response)["sent"] as? [Bool], [true, true])
        XCTAssertNil(host.finish())
    }

    func testWhatTheHostMayNotDoEndsTheSession() throws {
        let violations: [(String, String?)] = [
            ("a second request during a call", nil),
            ("an unknown frame", #"{"frame":"mystery"}"#),
            ("an answer to a request never issued", Self.answer("g99", "\"payload\":1")),
            ("a progress frame from the host", #"{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log"}"#),
            ("a malformed answer to the request waiting", Self.answer("g1", "\"payload\":1,\"extra\":1")),
            ("an answer with both payload and error", Self.answer("g1", #""payload":1,"error":{"code":"x","message":"m"}"#)),
            ("an answer with neither payload nor error", Self.answer("g1", "")),
            ("a frame that is not an object", "7"),
            ("an answer to the wrong kind of id", Self.answer("x1", "\"payload\":1")),
        ]
        for (what, frame) in violations {
            let host = try Self.started()
            let id = host.request("ask", #"{"op":"sign"}"#, progress: false)
            XCTAssertEqual(try host.receive()["id"] as? String, "g1")
            if let frame { host.send(frame) } else { host.request("events", "null", progress: true) }
            let failure = host.finish()
            XCTAssertEqual(failure as? JSONLineError, .protocolViolation, "\(what): \(String(describing: failure))")
            // The call that was in flight is never answered.
            while let frame = host.tryReceive(0.05) {
                let parsed = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(frame.utf8)) as? [String: Any])
                XCTAssertTrue(parsed["frame"] != nil || parsed["id"] as? String != id, "\(what): answered \(frame)")
            }
        }
    }

    func testTheConnectionEndingReleasesAWaitingHandlerAndTheCallIsStillAnswered() throws {
        let host = try Self.started()
        let id = host.request("ask", #"{"op":"sign"}"#, progress: false)
        XCTAssertEqual(try host.receive()["id"] as? String, "g1")
        // The host closes its end: the handler waiting for the answer is released, and the call is
        // still answered (the host may be reading), then the session ends.
        let finishing = Thread { _ = host.finish() }
        finishing.start()
        let exchange = try host.untilResponse(id)
        XCTAssertEqual(payload(exchange.response)["frame"] as? String, "closed")
        XCTAssertNil(host.finish())
    }

    func testARequestMayFollowTheHelloRightAway() throws {
        let host = try Host()
        let id = host.request("events", "null", progress: true)  // sent before the hello is answered
        XCTAssertEqual(try host.receive()["id"] as? String, "hello")
        XCTAssertEqual(payload(try host.untilResponse(id).response)["sent"] as? [Bool], [true, true])
        XCTAssertNil(host.finish())
    }

    func testWithoutAFrameChannelProgressIsUnsupportedAndThereIsNoCaller() throws {
        let guest = try Guest(descriptor: Self.descriptor, handler: Self.handle)
        func request(_ operation: String, progress: Bool) -> Data {
            Data(
                ("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"swift/frames\",\"revision\":\"r1\"},"
                    + "\"contract\":{\"name\":\"test.frames\",\"version\":\"v1\"},\"operation\":\"\(operation)\","
                    + "\"deadline\":\"2099-01-01T00:00:00Z\",\"payload\":{}" + (progress ? ",\"progress\":{}" : "") + "}").utf8)
        }
        // events reports, so without frames it fails: reporting is unsupported and the handler lets that escape.
        XCTAssertTrue(String(decoding: try guest.invoke(request("events", progress: true)), as: UTF8.self).contains("operation_failed"))
        let session = String(decoding: try guest.invoke(request("session", progress: false)), as: UTF8.self)
        XCTAssertTrue(session.contains("\"session\":false,\"reverse\":false"), session)
    }

    func testTheHelloMustBeBare() throws {
        let cases: [(String, String, Bool)] = [
            ("a bare hello, as the C engine sends it", Host.hello(), true),
            ("empty members", Host.hello(#""plugin":{"id":"","revision":""},"contract":{"name":"","version":""},"surface":"""#), true),
            ("a plugin named", Host.hello(#""plugin":{"id":"x","revision":""}"#), false),
            ("a surface named", Host.hello(#""surface":"x""#), false),
            ("a payload", Host.hello(#""payload":{}"#), false),
        ]
        for (what, frame, accepted) in cases {
            let host = try Host(hello: false)
            host.send(frame)
            if accepted {
                XCTAssertEqual(try host.receive()["id"] as? String, "hello", what)
                XCTAssertNil(host.finish(), what)
            } else {
                XCTAssertNotNil(host.finish(), what)
                XCTAssertNil(host.tryReceive(0.05), what)
            }
        }
    }
}
