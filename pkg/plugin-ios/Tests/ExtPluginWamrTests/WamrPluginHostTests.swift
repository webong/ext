import ExtPluginWamr
import Foundation
import XCTest

/// Runs the shared ext.conformance/v1 reactor (the Rust SDK's shared guest built for
/// wasm32-wasip1) on the in-process WAMR interpreter. Set EXT_RUST_REACTOR to the module.
final class WamrPluginHostTests: XCTestCase {
    private func reactor() throws -> Data {
        guard let path = ProcessInfo.processInfo.environment["EXT_RUST_REACTOR"], !path.isEmpty else {
            throw XCTSkip("set EXT_RUST_REACTOR to the wasm32-wasip1 build of the Rust shared example")
        }
        return try Data(contentsOf: URL(fileURLWithPath: path))
    }

    private func started(
        options: WamrPluginHost.Options = .init(), authorize: ((Data) -> Bool)? = nil, diagnostic: ((String) -> Void)? = nil
    ) throws -> WamrPluginHost {
        let module = try reactor()
        let descriptor = try WamrPluginHost.descriptor(of: module)
        let host = try WamrPluginHost(module: module, descriptor: descriptor, options: options, authorize: authorize, diagnostic: diagnostic)
        try host.start(timeout: 30)
        return host
    }

    private func request(_ operation: String, payload: String? = nil, id: String = "1", deadline: Date? = nil) -> Data {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let when = formatter.string(from: deadline ?? Date().addingTimeInterval(30))
        var body = #"{"apiVersion":"ext.plugin/v1","id":"\#(id)","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"\#(operation)","deadline":"\#(when)""#
        if let payload { body += #","payload":\#(payload)"# }
        return Data((body + "}").utf8)
    }

    private func text(_ data: Data) -> String { String(decoding: data, as: UTF8.self) }

    func testInspectionReturnsTheModulesOwnDescriptor() throws {
        let descriptor = text(try WamrPluginHost.descriptor(of: try reactor()))
        XCTAssertTrue(descriptor.contains(#""id":"ctx/conformance""#), descriptor)
        XCTAssertTrue(descriptor.contains(#""name":"public-error""#), descriptor)
    }

    func testEchoCarriesUnicodeAndLargeNumbers() throws {
        let host = try started()
        defer { host.close() }
        let payload = #"{"n":1.10,"big":12345678901234567890,"s":"héllo é 日本語 🙂","list":[1,{"x":null}]}"#
        let reply = text(try host.invoke(request("echo", payload: payload)))
        XCTAssertTrue(reply.contains(#""id":"1""#), reply)
        XCTAssertTrue(reply.contains("12345678901234567890"), reply)
        XCTAssertTrue(reply.contains("héllo é 日本語 🙂"), reply)
        XCTAssertTrue(reply.contains(#""list":[1,{"x":null}]"#), reply)
    }

    func testPublicErrorKeepsItsCodeAndPrivateErrorIsSanitized() throws {
        let host = try started()
        defer { host.close() }
        let busy = text(try host.invoke(request("public-error")))
        XCTAssertTrue(busy.contains(#""code":"busy""#), busy)
        let hidden = text(try host.invoke(request("private-error")))
        XCTAssertTrue(hidden.contains(#""error""#) && !hidden.lowercased().contains("transport"), hidden)
    }

    func testCallsAreAnsweredInOrderAndTheHostStaysUsable() throws {
        let host = try started()
        defer { host.close() }
        for i in 0..<50 {
            let reply = text(try host.invoke(request("echo", payload: "\(i)", id: "call-\(i)")))
            XCTAssertTrue(reply.contains(#""id":"call-\#(i)""#) && reply.contains(#""payload":\#(i)"#), reply)
        }
    }

    func testAMismatchedDescriptorIsRefusedAtStart() throws {
        let module = try reactor()
        let other = text(try WamrPluginHost.descriptor(of: module)).replacingOccurrences(of: "fixture-1", with: "fixture-2")
        let host = try WamrPluginHost(module: module, descriptor: Data(other.utf8))
        defer { host.close() }
        XCTAssertThrowsError(try host.start()) { error in
            XCTAssertEqual((error as? WamrPluginHost.Failure)?.status, 3, "\(error)") // EXT_MISMATCH
        }
    }

    func testAnAuthorizationDenialLeavesTheHostUsable() throws {
        var denyNext = true
        let host = try started(authorize: { _ in
            defer { denyNext = false }
            return !denyNext
        })
        defer { host.close() }
        XCTAssertThrowsError(try host.invoke(request("echo", payload: "1"))) { error in
            XCTAssertEqual((error as? WamrPluginHost.Failure)?.status, 2, "\(error)") // EXT_DENIED
        }
        XCTAssertTrue(text(try host.invoke(request("echo", payload: "2"))).contains(#""payload":2"#))
    }

    func testTheHostStopsAGuestAtItsRequestDeadline() throws {
        // Unlike a web view, the host enforces the request deadline itself: a guest that is still
        // running at the deadline is stopped and the instance ends, so the caller sees a timeout.
        let host = try started()
        defer { host.close() }
        let began = Date()
        XCTAssertThrowsError(try host.invoke(request("wait", deadline: Date().addingTimeInterval(0.4)), timeout: 10)) { error in
            XCTAssertEqual((error as? WamrPluginHost.Failure)?.status, 6, "\(error)") // EXT_TIMEOUT
        }
        let elapsed = Date().timeIntervalSince(began)
        XCTAssertGreaterThan(elapsed, 0.3)
        XCTAssertLessThan(elapsed, 5)
        XCTAssertThrowsError(try host.invoke(request("echo", payload: "1")), "the instance ends with the guest")
    }

    func testACallThatOverrunsItsTimeoutEndsTheInstance() throws {
        let host = try started()
        defer { host.close() }
        let began = Date()
        XCTAssertThrowsError(try host.invoke(request("wait", deadline: Date().addingTimeInterval(120)), timeout: 0.3)) { error in
            XCTAssertEqual((error as? WamrPluginHost.Failure)?.status, 6, "\(error)") // EXT_TIMEOUT
        }
        XCTAssertLessThan(Date().timeIntervalSince(began), 5, "the interpreter must be stopped at the deadline")
        XCTAssertThrowsError(try host.invoke(request("echo", payload: "1"))) { error in
            XCTAssertNotNil(error as? WamrPluginHost.Failure)
        }
    }

    func testCloseFromAnotherThreadInterruptsARunningCall() throws {
        let host = try started()
        DispatchQueue.global().asyncAfter(deadline: .now() + 0.3) { host.close() }
        let began = Date()
        XCTAssertThrowsError(try host.invoke(request("wait", deadline: Date().addingTimeInterval(120)), timeout: 60))
        XCTAssertLessThan(Date().timeIntervalSince(began), 5)
    }

    func testAModuleThatIsNotAReactorIsRefused() throws {
        // A valid module exporting only _start: a command.
        let command = Data([0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0, 7, 10, 1, 6, 95, 115, 116, 97, 114, 116, 0, 0, 10, 4, 1, 2, 0, 11])
        XCTAssertThrowsError(try WamrPluginHost.descriptor(of: command)) { error in
            XCTAssertEqual((error as? WamrPluginHost.Failure)?.status, 4, "\(error)") // EXT_UNSUPPORTED
        }
        XCTAssertThrowsError(try WamrPluginHost.descriptor(of: Data("not a module".utf8)))
        XCTAssertThrowsError(try WamrPluginHost(module: Data(), descriptor: Data("{}".utf8)))
    }

    func testAnInstructionBudgetStopsARunawayCall() throws {
        // The handshake needs between 20,000 and 50,000 instructions; this fixture's wait loop runs
        // about 150,000 per second, so 200,000 stops it in a second or two. The engine reports the
        // overrun of a call as an ordinary failure, not a distinct status.
        var options = WamrPluginHost.Options()
        options.instructionLimit = 200_000
        let host = try started(options: options)
        defer { host.close() }
        let began = Date()
        XCTAssertThrowsError(try host.invoke(request("wait", deadline: Date().addingTimeInterval(120)), timeout: 30))
        XCTAssertLessThan(Date().timeIntervalSince(began), 10, "the budget, not the 30 s timeout, must stop the call")
        XCTAssertThrowsError(try host.invoke(request("echo", payload: "1")), "the instance ends with the guest")
    }

    func testClosedHostRejectsCalls() throws {
        let host = try started()
        host.close()
        host.close()
        XCTAssertThrowsError(try host.invoke(request("echo", payload: "1"))) { error in
            XCTAssertEqual((error as? WamrPluginHost.Failure)?.status, 5, "\(error)") // EXT_CLOSED
        }
    }
}
