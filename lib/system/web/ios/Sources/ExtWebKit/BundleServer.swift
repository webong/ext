import Foundation
import Network

// An intentionally small HTTP/1.1 origin: one request per connection, no chunked
// request bodies or keep-alive. All mutable state belongs to queue. The root must
// remain immutable for a run (as with the Go runner, canonicalization is not a
// defense against a concurrent filesystem writer swapping symlinks).
final class BundleServer {
    private let queue = DispatchQueue(label: "ext.web.bundle")
    private let options: BundleRunner.Options
    private let root: URL
    private let token = UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
    private var listener: NWListener?
    private var connections: [UUID: NWConnection] = [:]
    private var timer: DispatchSourceTimer?
    private var origin: URL?
    private var host = ""
    private var ended = false
    private var began = ProcessInfo.processInfo.systemUptime
    private var seen: TimeInterval?
    private var outbox: [String] = []
    private var ready: (() -> Void)?
    private var console: ((BundleRunner.Event) -> Void)?
    private var message: ((String) -> Void)?
    private var finished: ((BundleRunner.Result) -> Void)?
    private static let maxFile = 64 << 20, maxMessage = 24 << 20

    init(options: BundleRunner.Options) throws {
        self.options = options
        root = options.root.standardizedFileURL.resolvingSymlinksInPath()
        var directory: ObjCBool = false
        guard options.root.isFileURL,
              FileManager.default.fileExists(atPath: root.path, isDirectory: &directory), directory.boolValue,
              options.timeout.isFinite, options.startupTimeout.isFinite, options.heartbeatTimeout.isFinite else {
            throw BundleRunner.Failure.invalidBundle
        }
        _ = try file(options.entry.isEmpty ? "index.html" : options.entry)
    }
    private func file(_ path: String) throws -> URL {
        let parts = path.split(separator: "/")
        guard !path.contains("\0"), !parts.contains(where: { $0.hasPrefix(".") && $0 != "." }) else {
            throw BundleRunner.Failure.invalidBundle
        }
        let url = root.appendingPathComponent(path.trimmingCharacters(in: CharacterSet(charactersIn: "/")))
            .standardizedFileURL.resolvingSymlinksInPath()
        guard url.path.hasPrefix(root.path == "/" ? "/" : root.path + "/"),
              let values = try? url.resourceValues(forKeys: [.isRegularFileKey, .fileSizeKey]),
              values.isRegularFile == true, (values.fileSize ?? Int.max) <= Self.maxFile else {
            throw BundleRunner.Failure.invalidBundle
        }
        return url
    }
    func start(open: @escaping (URL) -> Void, ready: @escaping () -> Void,
               console: @escaping (BundleRunner.Event) -> Void, message: @escaping (String) -> Void,
               finished: @escaping (BundleRunner.Result) -> Void) {
        queue.async {
            self.ready = ready; self.console = console; self.message = message; self.finished = finished
            guard !self.ended else {
                DispatchQueue.main.async { finished(.init(status: "closed", exitCode: 0, reason: "the host closed the run", url: nil)) }
                return
            }
            do {
                let parameters = NWParameters.tcp
                parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
                let listener = try NWListener(using: parameters)
                self.listener = listener
                listener.newConnectionHandler = { [weak self] connection in self?.accept(connection) }
                listener.stateUpdateHandler = { [weak self] state in
                    guard let self, !self.ended else { return }
                    switch state {
                    case .ready:
                        guard let port = listener.port else { return }
                        self.host = "127.0.0.1:\(port.rawValue)"
                        let url = URL(string: "http://\(self.host)/")!
                        self.origin = url
                        DispatchQueue.main.async { open(url) }
                    case .failed: self.end("failed", reason: "the loopback listener failed")
                    default: break
                    }
                }
                self.began = ProcessInfo.processInfo.systemUptime
                let timer = DispatchSource.makeTimerSource(queue: self.queue)
                self.timer = timer
                timer.schedule(deadline: .now() + 0.1, repeating: 0.1)
                timer.setEventHandler { [weak self] in self?.tick() }
                timer.resume()
                listener.start(queue: self.queue)
            } catch { self.end("failed", reason: "the loopback listener could not start") }
        }
    }
    func send(_ text: String) throws {
        guard text.utf8.count <= Self.maxMessage else { throw BundleRunner.Failure.messageTooLarge }
        try queue.sync {
            guard !ended else { throw BundleRunner.Failure.closed }
            guard outbox.count < 256 else { throw BundleRunner.Failure.queueFull }
            outbox.append(text)
        }
    }
    func finish(status: String, reason: String) { queue.async { self.end(status, reason: reason) } }
    func stop() { queue.async { self.end("closed", reason: "the host released the run") } }
    private func end(_ status: String, code: Int = 0, reason: String = "") {
        guard !ended else { return }
        ended = true
        timer?.cancel(); timer = nil
        listener?.cancel(); listener?.newConnectionHandler = nil; listener?.stateUpdateHandler = nil; listener = nil
        for connection in connections.values { connection.cancel() }; connections.removeAll(); outbox.removeAll()
        let result = BundleRunner.Result(status: status, exitCode: code, reason: reason, url: origin)
        if let finished { DispatchQueue.main.async { finished(result) } }
        finished = nil; ready = nil; console = nil; message = nil
    }
    private func tick() {
        let now = ProcessInfo.processInfo.systemUptime
        if options.timeout > 0 && now - began >= options.timeout { end("timeout", reason: "the run reached its time limit") }
        else if let seen {
            if now - seen > (options.heartbeatTimeout > 0 ? options.heartbeatTimeout : 15) { end("closed", reason: "the page stopped reporting") }
        } else if now - began > (options.startupTimeout > 0 ? options.startupTimeout : 30) { end("failed", reason: "the page did not load in time") }
    }
    private func touch() {
        let first = seen == nil
        seen = ProcessInfo.processInfo.systemUptime
        if first, let ready { DispatchQueue.main.async(execute: ready) }
    }
    private func accept(_ connection: NWConnection) {
        guard !ended, connections.count < 32 else { connection.cancel(); return }
        let id = UUID(); connections[id] = connection
        connection.start(queue: queue)
        // Bound slow/incomplete requests and long polls without retaining the server.
        queue.asyncAfter(deadline: .now() + 25) { [weak self, weak connection] in
            connection?.cancel(); self?.connections.removeValue(forKey: id)
        }
        read(connection, id: id, buffer: Data())
    }
    private func read(_ connection: NWConnection, id: UUID, buffer: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, complete, error in
            guard let self, self.connections[id] != nil else { return }
            var buffer = buffer; if let data { buffer.append(data) }
            guard buffer.count <= 2 * Self.maxMessage + 17408 else { self.reply(connection, id, 413); return }
            if let boundary = buffer.range(of: Data("\r\n\r\n".utf8)) {
                guard boundary.lowerBound <= 16384,
                      let header = String(data: buffer[..<boundary.lowerBound], encoding: .utf8),
                      let request = Request(header) else { self.reply(connection, id, 400); return }
                let limit = request.path == "/__ext/\(self.token)/host" ? 2 * Self.maxMessage + 1024 : 256 << 10
                guard request.length <= limit else { self.reply(connection, id, 413); return }
                let body = buffer[boundary.upperBound...]
                if body.count >= request.length {
                    self.handle(request, body: Data(body.prefix(request.length)), connection: connection, id: id); return
                }
            } else if buffer.count > 16384 { self.reply(connection, id, 431); return }
            if complete || error != nil { connection.cancel(); self.connections.removeValue(forKey: id) }
            else { self.read(connection, id: id, buffer: buffer) }
        }
    }
    private struct Report: Decodable {
        var level: String?, text: String?, data: String?, code: Int?
    }
    private struct Request {
        let method: String, path: String
        let headers: [String: String]
        let length: Int
        init?(_ text: String) {
            let lines = text.components(separatedBy: "\r\n")
            let first = (lines.first ?? "").split(separator: " ")
            guard first.count == 3, first[2] == "HTTP/1.1" || first[2] == "HTTP/1.0",
                  first[1].hasPrefix("/"), let decoded = first[1].split(separator: "?", maxSplits: 1).first?.removingPercentEncoding else { return nil }
            method = String(first[0]); path = decoded
            var values: [String: String] = [:]
            for line in lines.dropFirst() {
                guard let colon = line.firstIndex(of: ":") else { return nil }
                let key = line[..<colon].lowercased()
                guard values[key] == nil else { return nil }
                values[key] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
            }
            guard values["transfer-encoding"] == nil, let size = Int(values["content-length"] ?? "0"), size >= 0 else { return nil }
            length = size; headers = values
        }
    }
    private func handle(_ r: Request, body: Data, connection c: NWConnection, id: UUID) {
        guard r.headers["host"] == host else { reply(c, id, 421); return }
        let prefix = "/__ext/\(token)/"
        if r.path.hasPrefix("/__ext/") {
            guard r.path.hasPrefix(prefix) else { reply(c, id, 404); return }
            let name = String(r.path.dropFirst(prefix.count))
            if name == "shim.js" && r.method == "GET" {
                reply(c, id, 200, Data(BundleAssets.shim.replacingOccurrences(of: "%TOKEN%", with: token).utf8), "text/javascript; charset=utf-8"); return
            }
            guard (r.headers["origin"] ?? "http://" + host) == "http://" + host,
                  (r.headers["sec-fetch-site"] ?? "same-origin") == "same-origin" else { reply(c, id, 403); return }
            if name == "next" && r.method == "GET" { touch(); poll(c, id, until: ProcessInfo.processInfo.systemUptime + 20); return }
            guard r.method == "POST" else { reply(c, id, 403); return }
            guard let value = try? JSONDecoder().decode(Report.self, from: body.isEmpty ? Data("{}".utf8) : body) else { reply(c, id, 400); return }
            switch name {
            case "alive": touch()
            case "log":
                touch()
                let level = value.level ?? "log", text = value.text ?? ""
                let limited = text.utf8.count > 65536 ? String(decoding: Array(text.utf8.prefix(65536)), as: UTF8.self) + "…" : text
                let event = BundleRunner.Event(level: ["log", "info", "warn", "error", "debug", "exception"].contains(level) ? level : "log", text: limited)
                if let console { DispatchQueue.main.async { console(event) } }
            case "host":
                let text = value.data ?? ""
                guard text.utf8.count <= Self.maxMessage else { reply(c, id, 413); return }
                touch(); if let message { DispatchQueue.main.async { message(text) } }
            case "exit":
                touch(); connections.removeValue(forKey: id); reply(c, id, 204)
                end("exited", code: value.code ?? 0); return
            case "closed":
                connections.removeValue(forKey: id); reply(c, id, 204)
                end("closed", reason: "the page was closed"); return
            default: reply(c, id, 404); return
            }
            reply(c, id, 204); return
        }
        guard r.method == "GET" || r.method == "HEAD" else { reply(c, id, 405); return }
        do {
            let entry = options.entry.isEmpty ? "index.html" : options.entry
            let path = r.path == "/" ? entry : r.path
            let url = try file(path)
            var type = mime(url.pathExtension.lowercased())
            var data: Data
            if r.path == "/" && ["js", "mjs"].contains(url.pathExtension.lowercased()) {
                let escaped = entry.split(separator: "/").map { String($0).addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? "" }.joined(separator: "/")
                data = Data("<!doctype html><html><head></head><body><script type=\"module\" src=\"/\(escaped)\"></script></body></html>".utf8)
                type = "text/html; charset=utf-8"
            } else { data = try Data(contentsOf: url); guard data.count <= Self.maxFile else { reply(c, id, 413); return } }
            if type.hasPrefix("text/html") {
                var html = String(decoding: data, as: UTF8.self)
                let tag = "<script src=\"\(prefix)shim.js\"></script>"
                if let range = html.range(of: "<head(?:\\s[^>]*)?>", options: [.regularExpression, .caseInsensitive]) { html.insert(contentsOf: tag, at: range.upperBound) }
                else { html = tag + html }
                data = Data(html.utf8)
            }
            reply(c, id, 200, data, type, head: r.method == "HEAD")
        } catch { reply(c, id, 404) }
    }
    private func poll(_ c: NWConnection, _ id: UUID, until: TimeInterval) {
        guard !ended, connections[id] != nil else { return }
        if !outbox.isEmpty { reply(c, id, 200, Data(outbox.removeFirst().utf8), "text/plain; charset=utf-8") }
        else if ProcessInfo.processInfo.systemUptime >= until { reply(c, id, 204) }
        else { queue.asyncAfter(deadline: .now() + 0.1) { [weak self] in self?.poll(c, id, until: until) } }
    }
    private func reply(_ c: NWConnection, _ id: UUID, _ status: Int, _ data: Data = Data(), _ type: String = "text/plain", head: Bool = false) {
        var header = "HTTP/1.1 \(status) Response\r\nConnection: close\r\nContent-Length: \(data.count)\r\nContent-Type: \(type)\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\nReferrer-Policy: no-referrer\r\nContent-Security-Policy: \(options.policy.contentSecurityPolicy)\r\n"
        if options.crossOriginIsolation { header += "Cross-Origin-Opener-Policy: same-origin\r\nCross-Origin-Embedder-Policy: require-corp\r\nCross-Origin-Resource-Policy: same-origin\r\n" }
        var response = Data((header + "\r\n").utf8); if !head { response.append(data) }
        c.send(content: response, completion: .contentProcessed { [weak self] _ in c.cancel(); self?.connections.removeValue(forKey: id) })
    }
    private func mime(_ ext: String) -> String {
        ["html": "text/html; charset=utf-8", "htm": "text/html; charset=utf-8", "js": "text/javascript; charset=utf-8",
         "mjs": "text/javascript; charset=utf-8", "css": "text/css", "json": "application/json", "wasm": "application/wasm",
         "svg": "image/svg+xml", "png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif",
         "webp": "image/webp", "woff": "font/woff", "woff2": "font/woff2", "mp4": "video/mp4", "txt": "text/plain"] [ext] ?? "application/octet-stream"
    }
}
