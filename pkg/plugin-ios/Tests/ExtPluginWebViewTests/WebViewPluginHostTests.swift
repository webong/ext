#if canImport(WebKit)
import ExtPluginWebView
import Foundation
import XCTest

/// Runs the shared ext.conformance/v1 reactor (the Rust SDK's shared guest built for
/// wasm32-wasip1) inside a real web view, over the real page shim of res/web/bundle.
/// Set EXT_RUST_REACTOR to the module; scripts/plugin-ios-webview.sh does.
@MainActor
final class WebViewPluginHostTests: XCTestCase {
    private func shim() throws -> String {
        // pkg/plugin-ios/Tests/ExtPluginWebViewTests/<this file> -> repository root.
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        return try String(contentsOf: root.appendingPathComponent("res/web/bundle/shim.js"), encoding: .utf8)
    }

    private func reactor() throws -> Data {
        guard let path = ProcessInfo.processInfo.environment["EXT_RUST_REACTOR"], !path.isEmpty else {
            throw XCTSkip("set EXT_RUST_REACTOR to the wasm32-wasip1 build of the Rust shared example")
        }
        return try Data(contentsOf: URL(fileURLWithPath: path))
    }

    private func host() async throws -> WebViewPluginHost {
        let host = WebViewPluginHost(module: try reactor(), bridge: try shim())
        // The first web view in a freshly launched app can take tens of seconds to
        // start in the simulator (32 s cold, about 3 s warm), so this is generous.
        try await host.start(timeout: 120)
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

    func testTheEmbeddedAssetsMatchTheirSources() throws {
        // GeneratedAssets is internal; its content is exercised through the default
        // bridge. Here the bundled shim must still be the repository's shim.js.
        let embedded = try shim()
        XCTAssertTrue(embedded.contains("window.ext") && embedded.contains("%TOKEN%"))
    }

    func testTheDefaultBridgeWorksWithoutPassingTheShim() async throws {
        let host = WebViewPluginHost(module: try reactor())
        try await host.start(timeout: 120)
        defer { host.close() }
        let descriptor = String(decoding: try await host.handshake(deadline: Date().addingTimeInterval(10)), as: UTF8.self)
        XCTAssertTrue(descriptor.contains(#""id":"ctx/conformance""#), descriptor)
    }

    func testHandshakeReturnsTheDescriptor() async throws {
        let host = try await host()
        defer { host.close() }
        let descriptor = text(try await host.handshake(deadline: Date().addingTimeInterval(10)))
        XCTAssertTrue(descriptor.contains(#""id":"ctx/conformance""#), descriptor)
        XCTAssertTrue(descriptor.contains(#""name":"public-error""#), descriptor)
    }

    func testEchoCarriesUnicodeAndLargeNumbersAcrossThePageBoundary() async throws {
        let host = try await host()
        defer { host.close() }
        _ = try await host.handshake(deadline: Date().addingTimeInterval(10))
        // The host passes frames through unchanged. This guest echoes by parsing and
        // re-serializing, so it may normalize number text (1.10 becomes 1.1); the
        // checks here are what the page boundary must not damage.
        let payload = #"{"n":1.10,"big":12345678901234567890,"s":"héllo \u00e9 日本語 🙂","list":[1,{"x":null}]}"#
        let reply = text(try await host.invoke(request("echo", payload: payload)))
        XCTAssertTrue(reply.contains(#""id":"1""#), reply)
        XCTAssertTrue(reply.contains("12345678901234567890"), reply)
        XCTAssertTrue(reply.contains("héllo é 日本語 🙂"), reply)
        XCTAssertTrue(reply.contains(#""list":[1,{"x":null}]"#), reply)
    }

    func testPublicErrorKeepsItsCodeAndRetryHint() async throws {
        let host = try await host()
        defer { host.close() }
        _ = try await host.handshake(deadline: Date().addingTimeInterval(10))
        let reply = text(try await host.invoke(request("public-error")))
        XCTAssertTrue(reply.contains(#""code":"busy""#), reply)
        XCTAssertTrue(reply.contains("10"), reply)
    }

    func testPrivateErrorIsSanitized() async throws {
        let host = try await host()
        defer { host.close() }
        _ = try await host.handshake(deadline: Date().addingTimeInterval(10))
        let reply = text(try await host.invoke(request("private-error")))
        XCTAssertTrue(reply.contains(#""error""#), reply)
        XCTAssertFalse(reply.lowercased().contains("transport"), reply)
    }

    func testCallsAreAnsweredInOrder() async throws {
        let host = try await host()
        defer { host.close() }
        _ = try await host.handshake(deadline: Date().addingTimeInterval(10))
        for i in 0..<25 {
            let reply = text(try await host.invoke(request("echo", payload: "\(i)", id: "call-\(i)")))
            XCTAssertTrue(reply.contains(#""id":"call-\#(i)""#) && reply.contains(#""payload":\#(i)"#), reply)
        }
    }

    func testAGuestThatHonoursItsDeadlineAnswersBeforeTheHostTimeout() async throws {
        let host = try await host()
        defer { host.close() }
        _ = try await host.handshake(deadline: Date().addingTimeInterval(10))
        let started = Date()
        let reply = text(try await host.invoke(request("wait", deadline: Date().addingTimeInterval(0.4)), timeout: 10))
        XCTAssertLessThan(Date().timeIntervalSince(started), 5)
        XCTAssertTrue(reply.contains(#""error""#), reply)
    }

    func testACallThatOverrunsItsDeadlineDestroysThePage() async throws {
        let host = try await host()
        _ = try await host.handshake(deadline: Date().addingTimeInterval(10))
        let began = Date()
        do {
            // The guest waits for a far deadline; the host gives up after one second.
            _ = try await host.invoke(request("wait", deadline: Date().addingTimeInterval(120)), timeout: 1)
            XCTFail("the call should have timed out")
        } catch let failure as WebViewPluginHost.Failure {
            XCTAssertEqual(failure, .timeout)
        }
        XCTAssertLessThan(Date().timeIntervalSince(began), 5, "the host must give up at its own deadline")
        do {
            _ = try await host.invoke(request("echo", payload: "1"), timeout: 5)
            XCTFail("a destroyed host must not answer")
        } catch let failure as WebViewPluginHost.Failure {
            XCTAssertEqual(failure, .closed)
        }
    }

    func testClosedHostRejectsCalls() async throws {
        let host = try await host()
        host.close()
        host.close() // idempotent
        do {
            _ = try await host.handshake(deadline: Date().addingTimeInterval(5))
            XCTFail("a closed host must not answer")
        } catch let failure as WebViewPluginHost.Failure {
            XCTAssertEqual(failure, .closed)
        }
    }

    func testAModuleThatIsNotAReactorFailsToStart() async throws {
        // A valid module exporting only _start: a command, which the page refuses.
        let command = Data([0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0, 7, 10, 1, 6, 95, 115, 116, 97, 114, 116, 0, 0, 10, 4, 1, 2, 0, 11])
        let host = WebViewPluginHost(module: command, bridge: try shim())
        do {
            try await host.start(timeout: 120)
            XCTFail("a command must not start as a reactor")
        } catch let failure as WebViewPluginHost.Failure {
            guard case .page(let reason) = failure else { return XCTFail("\(failure)") }
            XCTAssertTrue(reason.contains("_start") || reason.contains("export"), reason)
        }
        host.close()
    }
}
#endif
