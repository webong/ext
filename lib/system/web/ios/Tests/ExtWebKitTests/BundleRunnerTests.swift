import XCTest
import WebKit
import ExtWeb
@testable import ExtWebKit
#if canImport(UIKit)
import UIKit
#endif

@MainActor
final class BundleRunnerTests: XCTestCase {
    #if canImport(UIKit)
    private var window: UIWindow?
    #endif
    private func attach(_ view: WKWebView) {
        #if canImport(UIKit)
        let w = UIWindow(frame: UIScreen.main.bounds), controller = UIViewController()
        controller.view = view; w.rootViewController = controller; w.makeKeyAndVisible(); window = w
        #endif
    }
    private func root(_ files: [String: String]) throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        for (name, value) in files { try Data(value.utf8).write(to: root.appendingPathComponent(name)) }
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        return root
    }
    func testModuleWorkerCSPMessagingAndExit() async throws {
        let directory = try root([
            "dep.mjs": "export const value = 7;",
            "worker.js": "postMessage(42);",
            "main.mjs": """
            import {value} from './dep.mjs';
            console.info('module-loaded');
            let blocked = false;
            try { await fetch('https://example.invalid/blocked'); } catch (_) { blocked = true; }
            const hidden = await fetch('/.secret');
            const worker = new Worker('/worker.js');
            const answer = await new Promise(resolve => worker.onmessage = e => resolve(e.data));
            worker.terminate();
            ext.host.send(JSON.stringify({blocked, hidden: hidden.status, answer, transport: ext.host.transport}));
            ext.host.onmessage = text => { console.log(text); ext.exit(text === 'host-message' && blocked && hidden.status === 404 && answer === 42 ? value : 99); };
            """, ".secret": "private"])
        var options = BundleRunner.Options(root: directory); options.entry = "main.mjs"; options.timeout = 15
        let runner = try BundleRunner(options: options); attach(runner.webView)
        defer { runner.close() }
        var logs: [String] = [], messages: [String] = [], ready = 0
        runner.onConsole = { logs.append($0.text) }
        runner.onMessage = { messages.append($0) }
        runner.onReady = { ready += 1; try! runner.send("host-message") }
        let result = try await runner.run()
        XCTAssertEqual(result.status, "exited", result.reason); XCTAssertEqual(result.exitCode, 7)
        XCTAssertEqual(ready, 1); XCTAssertTrue(logs.contains("module-loaded")); XCTAssertTrue(logs.contains("host-message"))
        XCTAssertEqual(messages.count, 1); XCTAssertTrue(messages.first?.contains("http") == true)
        XCTAssertThrowsError(try runner.send("late"))
        do { _ = try await runner.run(); XCTFail("runner reused") } catch { }
    }
    func testHTMLTimeoutAndCancellation() async throws {
        let directory = try root(["index.html": "<html><head><script>console.log('first-script'); ext.exit(0)</script></head></html>"])
        var options = BundleRunner.Options(root: directory); options.timeout = 15
        let runner = try BundleRunner(options: options); attach(runner.webView)
        var first = false; runner.onConsole = { if $0.text == "first-script" { first = true } }
        let result = try await runner.run(); XCTAssertEqual(result.status, "exited"); XCTAssertTrue(first)
        try Data("<head></head>".utf8).write(to: directory.appendingPathComponent("index.html"))
        options.timeout = 0.3
        let timed = try BundleRunner(options: options); attach(timed.webView)
        let timeout = try await timed.run(); XCTAssertEqual(timeout.status, "timeout")
        options.timeout = 15
        let cancelled = try BundleRunner(options: options); attach(cancelled.webView)
        let task = Task { try await cancelled.run() }
        task.cancel()
        let closed = try await task.value; XCTAssertEqual(closed.status, "closed")
    }
    func testConfinementAndProtocol() async throws {
        let directory = try root(["index.html": "<head></head>", ".secret": "secret"])
        let outside = directory.deletingLastPathComponent().appendingPathComponent(UUID().uuidString)
        try Data("outside".utf8).write(to: outside)
        defer { try? FileManager.default.removeItem(at: outside) }
        try FileManager.default.createSymbolicLink(at: directory.appendingPathComponent("escape.txt"), withDestinationURL: outside)
        var options = BundleRunner.Options(root: directory); options.entry = "escape.txt"
        XCTAssertThrowsError(try BundleRunner(options: options))
        options.entry = "../index.html"; XCTAssertThrowsError(try BundleRunner(options: options))
        options.entry = "index.html"; options.crossOriginIsolation = true
        let server = try BundleServer(options: options)
        defer { server.stop() }
        let url: URL = await withCheckedContinuation { c in
            server.start(open: { c.resume(returning: $0) }, ready: {}, console: { _ in }, message: { _ in }, finished: { _ in })
        }
        let (html, response) = try await URLSession.shared.data(from: url)
        let http = response as! HTTPURLResponse
        XCTAssertEqual(http.value(forHTTPHeaderField: "Cross-Origin-Embedder-Policy"), "require-corp")
        XCTAssertEqual(http.value(forHTTPHeaderField: "Content-Security-Policy"), options.policy.contentSecurityPolicy)
        let text = String(decoding: html, as: UTF8.self)
        let prefix = String(text.split(separator: "\"")[1]).replacingOccurrences(of: "shim.js", with: "")
        for path in ["/.secret", "/escape.txt", "/%2e%2e/outside", "/__ext/bad/alive"] {
            let (_, response) = try await URLSession.shared.data(from: URL(string: path, relativeTo: url)!)
            XCTAssertEqual((response as! HTTPURLResponse).statusCode, 404, path)
        }
        var request = URLRequest(url: URL(string: prefix + "alive", relativeTo: url)!)
        request.httpMethod = "POST"; request.httpBody = Data("{}".utf8)
        request.setValue("https://other.invalid", forHTTPHeaderField: "Origin")
        let (_, denied) = try await URLSession.shared.data(for: request)
        XCTAssertEqual((denied as! HTTPURLResponse).statusCode, 403)
        request.setValue(nil, forHTTPHeaderField: "Origin")
        request.url = URL(string: prefix + "exit", relativeTo: url)!
        request.httpBody = Data("{\"code\":\"wrong-type\"}".utf8)
        let (_, malformed) = try await URLSession.shared.data(for: request)
        XCTAssertEqual((malformed as! HTTPURLResponse).statusCode, 400)
        request.httpBody = Data("{}".utf8)
        request.setValue("rebound.invalid", forHTTPHeaderField: "Host")
        let (_, rebound) = try await URLSession.shared.data(for: request)
        XCTAssertEqual((rebound as! HTTPURLResponse).statusCode, 421)
        for _ in 0..<256 { try server.send("queued") }
        XCTAssertThrowsError(try server.send("overflow"))
    }
    func testStartupAndHeartbeat() async throws {
        let directory = try root(["index.html": "<head></head>"])
        var options = BundleRunner.Options(root: directory); options.startupTimeout = 0.2
        let server = try BundleServer(options: options)
        let result: BundleRunner.Result = await withCheckedContinuation { c in
            server.start(open: { _ in }, ready: {}, console: { _ in }, message: { _ in }, finished: { c.resume(returning: $0) })
        }
        XCTAssertEqual(result.status, "failed")
        options.heartbeatTimeout = 0.2
        let heartbeat = try BundleServer(options: options)
        let ended = expectation(description: "heartbeat ended")
        var status = ""
        let url: URL = await withCheckedContinuation { c in
            heartbeat.start(open: { c.resume(returning: $0) }, ready: {}, console: { _ in }, message: { _ in }, finished: { status = $0.status; ended.fulfill() })
        }
        let (html, _) = try await URLSession.shared.data(from: url)
        let path = String(String(decoding: html, as: UTF8.self).split(separator: "\"")[1]).replacingOccurrences(of: "shim.js", with: "alive")
        var request = URLRequest(url: URL(string: path, relativeTo: url)!); request.httpMethod = "POST"
        _ = try await URLSession.shared.data(for: request)
        await fulfillment(of: [ended], timeout: 3)
        XCTAssertEqual(status, "closed")
    }
}
