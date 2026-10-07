import CExtEngine
import ExtPluginGuest
import XCTest

final class GuestTests: XCTestCase {
    private let descriptor = Descriptor(
        identity: Identity(id: "swift/test", revision: "r1"),
        contracts: [Contract(name: "test", version: "v1", operations: [Operation(name: "echo"), Operation(name: "boom"), Operation(name: "busy"), Operation(name: "wait")])])

    private func request(_ operation: String, payload: String? = nil, deadline: String = "2099-01-01T00:00:00Z") -> Data {
        var body = #"{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"swift/test","revision":"r1"},"contract":{"name":"test","version":"v1"},"operation":"\#(operation)","deadline":"\#(deadline)""#
        if let payload { body += #","payload":\#(payload)"# }
        return Data((body + "}").utf8)
    }

    private func guest() throws -> Guest {
        try Guest(descriptor: descriptor) { call in
            switch call.operation {
            case "boom": throw NSError(domain: "secret", code: 1)
            case "busy": throw RemoteError(code: "busy", message: "try later", retryAfterMilliseconds: 10)
            case "wait":
                while true { try call.checkDeadline(); usleep(1000) }
            default: return call.payload
            }
        }
    }

    private func text(_ data: Data) -> String { String(decoding: data, as: UTF8.self) }

    func testDescriptorIsServedByTheEngine() throws {
        let json = try text(guest().descriptorJSON())
        XCTAssertTrue(json.contains(#""apiVersion":"ext.plugin/v1""#), json)
        XCTAssertTrue(json.contains(#""id":"swift/test""#), json)
        XCTAssertTrue(json.contains(#""name":"echo""#), json)
    }

    func testEchoReturnsThePayloadWithoutReencodingNumbers() throws {
        let payload = #"{"n":1.10,"big":12345678901234567890,"s":"é\n","list":[1,{"x":null}]}"#
        let response = try text(guest().invoke(request("echo", payload: payload)))
        XCTAssertTrue(response.contains(#""payload":\#(payload)"#), response)
        XCTAssertTrue(response.contains(#""id":"1""#), response)
    }

    func testAbsentPayloadBecomesNull() throws {
        let response = try text(guest().invoke(request("echo")))
        XCTAssertTrue(response.contains(#""payload":null"#), response)
    }

    func testPublicErrorKeepsCodeAndRetryHint() throws {
        let response = try text(guest().invoke(request("busy")))
        XCTAssertTrue(response.contains(#""code":"busy""#), response)
        XCTAssertTrue(response.contains(#""retryAfterMilliseconds":10"#), response)
        XCTAssertFalse(response.contains("payload"), response)
    }

    func testPrivateErrorIsSanitized() throws {
        let response = try text(guest().invoke(request("boom")))
        XCTAssertTrue(response.contains(#""code":"operation_failed""#), response)
        XCTAssertFalse(response.contains("secret"), response)
    }

    func testRequestsOutsideTheDescriptorAreRejectedByTheEngine() throws {
        let response = try text(guest().invoke(request("missing")))
        XCTAssertTrue(response.contains("invalid_request"), response)
    }

    func testMalformedRequestIsATransportFailure() throws {
        XCTAssertThrowsError(try guest().invoke(Data("{".utf8))) { error in
            guard case GuestError.engine(let status) = error else { return XCTFail("\(error)") }
            XCTAssertNotEqual(status, 0)
        }
    }

    func testExpiredDeadlineFailsWithoutRunningTheHandler() throws {
        let response = try text(guest().invoke(request("echo", payload: "1", deadline: "2001-01-01T00:00:00Z")))
        XCTAssertTrue(response.contains("operation_failed"), response)
    }

    func testCooperativeWaitStopsAtTheDeadline() throws {
        let started = Date()
        let response = try text(guest().invoke(request("wait"), timeoutMilliseconds: 80))
        XCTAssertLessThan(Date().timeIntervalSince(started), 2)
        XCTAssertTrue(response.contains("operation_failed"), response)
    }

    func testConcurrentInvocations() throws {
        let guest = try guest()
        let failures = NSLock()
        var bad = 0
        DispatchQueue.concurrentPerform(iterations: 200) { i in
            let ok = (try? guest.invoke(self.request("echo", payload: "\(i)"))).map { self.text($0).contains(#""payload":\#(i)"#) } ?? false
            if !ok { failures.lock(); bad += 1; failures.unlock() }
        }
        XCTAssertEqual(bad, 0)
    }
}
