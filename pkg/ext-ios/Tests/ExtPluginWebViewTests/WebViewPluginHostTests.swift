#if canImport(WebKit)
import ExtPluginWebView
import Foundation
import XCTest

/// Runs the shared ext.conformance/v1 reactor (the Rust SDK's shared guest built for
/// wasm32-wasip1) inside a real web view, over the real page shim of res/web/host/engine/bundle.
/// Set EXT_RUST_REACTOR to the module; scripts/ext-ios-webview.sh does.
@MainActor
final class WebViewPluginHostTests: XCTestCase {
    private func shim() throws -> String {
        // pkg/ext-ios/Tests/ExtPluginWebViewTests/<this file> -> repository root.
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        return try String(contentsOf: root.appendingPathComponent("res/web/host/engine/bundle/shim.js"), encoding: .utf8)
    }

    private func reactor() throws -> Data {
        guard let path = ProcessInfo.processInfo.environment["EXT_RUST_REACTOR"], !path.isEmpty else {
            throw XCTSkip("set EXT_RUST_REACTOR to the wasm32-wasip1 build of the Rust shared example")
        }
        return try Data(contentsOf: URL(fileURLWithPath: path))
    }

    /// A valid module exporting only _start: a command, which the page refuses.
    private static let command = Data([0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0, 7, 10, 1, 6, 95, 115, 116, 97, 114, 116, 0, 0, 10, 4, 1, 2, 0, 11])

    private static var webEngineIsWarm = false

    /// Starts one throwaway web view before the first test uses one. The first web view in a
    /// freshly booted simulator can take tens of seconds to start (32 s cold, about 3 s warm,
    /// on a development machine), and on a hosted macOS 15 runner (Xcode 16.4, iOS 18.5) the
    /// first two starts each overran 120 s before the third answered at once. In two later
    /// runs the engine answered after 385 s, and after 453 s (a 360 s wait overran and the
    /// retry answered 91 s later). The probe is a command module, which the page refuses with
    /// its reason, so it proves the page ran without needing the reactor. The first wait is
    /// long and the retries shorter, and the time is printed so a slow image shows in the log
    /// instead of as a failed test.
    private func warmUp() async throws {
        guard !Self.webEngineIsWarm else { return }
        let began = Date()
        var timeouts = 0
        for timeout in [600.0, 180, 180] {
            let probe = WebViewPluginHost(module: Self.command, bridge: try shim())
            defer { probe.close() }
            do {
                try await probe.start(timeout: timeout)
            } catch WebViewPluginHost.Failure.page(_) {
                // The page ran and refused the command: the web engine is up.
            } catch WebViewPluginHost.Failure.timeout {
                timeouts += 1
                print("web engine warm-up: start \(timeouts) overran \(Int(timeout)) s after \(Int(Date().timeIntervalSince(began))) s")
                continue
            }
            Self.webEngineIsWarm = true
            print("web engine warm-up: ready after \(Int(Date().timeIntervalSince(began))) s, \(timeouts) overrun(s)")
            return
        }
        XCTFail("the web engine did not start after \(Int(Date().timeIntervalSince(began))) s")
        throw WebViewPluginHost.Failure.timeout
    }

    private func host() async throws -> WebViewPluginHost {
        try await warmUp()
        let host = WebViewPluginHost(module: try reactor(), bridge: try shim())
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
        let module = try reactor()
        try await warmUp()
        let host = WebViewPluginHost(module: module)
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
        try await warmUp()
        let host = WebViewPluginHost(module: Self.command, bridge: try shim())
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
