import ExtPluginStore
import Foundation
import Network
import Security
import XCTest

/// The download fixtures every language runs (lib/plugin/testdata/v1/download-cases.json), through
/// `ArchiveDownloader` against a local HTTPS server on Network.framework, with a plain HTTP server
/// beside it that must never receive a request. The server's identity is a self-signed test
/// certificate for 127.0.0.1, below; the downloader trusts it through `serverTrust`, evaluated at a
/// fixed date inside its validity so the test does not expire with it. Importing the identity
/// without a keychain needs macOS 15 or iOS 18 (`kSecImportToMemoryOnly`); the test skips before.
final class DownloadCasesTests: XCTestCase {
    private static var fixtures: URL {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("lib/plugin/testdata/v1")
    }

    /// A PKCS #12 of an EC P-256 key and a self-signed certificate for IP 127.0.0.1 and localhost
    /// (serverAuth, valid from 2026-11-17 to 2029-02-19), password "ext-test". Test-only: it
    /// protects nothing.
    private static let identityP12 = """
        MIID0gIBAzCCA5gGCSqGSIb3DQEHAaCCA4kEggOFMIIDgTCCAncGCSqGSIb3DQEHBqCCAmgwggJk
        AgEAMIICXQYJKoZIhvcNAQcBMBwGCiqGSIb3DQEMAQYwDgQIDGuTv8MPM3QCAggAgIICMDIgncOv
        AUi5CSPwcVMz8ayunLAa0LSbS99147sVWGT5meM1AKnBmEPdt5ckeFizZu8PDtoF/13YctveLUOc
        IIlgmMYrNkLZGzvkv7sQZrLRJJtXElcn1Y5iMmI8HdAgaVi4HZkMSNgaOBna5KYmI7E2XGygkhF3
        ae9gHySDiI68JxiDvYCL2CfRUvWNZcmfVEB00LD9ZPXiCx0UMKkzAPXLfOZyqA+4fv3wYugLbstO
        VqzS857Ry9WfPKYd636a3UXddF3Si5zIME/Swh7XWhbLHGGCAd8bLn2YjtEBUSbJxxD22r8Y0S7w
        6x2+oT6jveU/PzogpX+GwJQ8aVDXPxBlGGukYzWaU8/RZHhSqi1d6mJC/9jznLGb7yo4M8q8vNzE
        jqSVRfspQsNCRpYoFl5wAZRKGEtkRRjdduxgKJ0iqtt37BSSZByZCQfNB9x113tycFGcgD7Z802T
        9AMFNHrxvd6SJYSjIuCORl177ktNC2DzAEXPCifNvFbqBsCCtck+/95ZTS7xqbV+gCW4a7TJ2IAG
        3YpwBF5S510wRIvh1g2c9+HvvhjJvFjHMnH1ClKrMImh2mAfOFOEUiTad4FAazZA6W+ypEONNkcE
        toHXhrzYtfY4X0Z1LYAP4oPmrt83CLMWux6bkQde4ZTEN7Yv0S1keIQm27kRxv6yd/0DzL2i62IS
        Z0vQtYEt/4SnuandZ4AOuUBy6r9A7aMVULgZAd1NBphSTUN1UNhXc1hHMIIBAgYJKoZIhvcNAQcB
        oIH0BIHxMIHuMIHrBgsqhkiG9w0BDAoBAqCBtDCBsTAcBgoqhkiG9w0BDAEDMA4ECFy2AMysURom
        AgIIAASBkLnNXUqG7Y83gw+JmhhWoOsStz9Fq2y0aSWlM9etPztCyxvRbL6sd+wd8dT3oJvSI2xG
        GWLNss6FKFUs80ldvDklnlSjuCb4cs23I5gzulOiW6+WeZqYZloisNNXeR5vRoH7RIRCM0Ca/bsa
        zrLQo/sI2GMJD84H64uGdiQ6OpVjDfK6aOl+bek3CpyKussJGDElMCMGCSqGSIb3DQEJFTEWBBQz
        KDE9PWQiV2tlIv8JVXSWqOw2UjAxMCEwCQYFKw4DAhoFAAQUI4LGU02OxlNjmjPgeLaiowisemAE
        CDrHc4Uuurz3AgIIAA==
        """

    private struct Identity {
        let identity: SecIdentity
        let certificate: SecCertificate
    }

    private func identity() throws -> Identity {
        guard #available(macOS 15.0, iOS 18.0, *) else {
            throw XCTSkip("importing a test identity without a keychain needs macOS 15 or iOS 18")
        }
        let data = try XCTUnwrap(Data(base64Encoded: Self.identityP12, options: .ignoreUnknownCharacters))
        let options: [String: Any] = [kSecImportExportPassphrase as String: "ext-test",
                                      kSecImportToMemoryOnly as String: true]
        var items: CFArray?
        let status = SecPKCS12Import(data as CFData, options as CFDictionary, &items)
        XCTAssertEqual(status, errSecSuccess, "SecPKCS12Import")
        let first = try XCTUnwrap((items as? [[String: Any]])?.first)
        let identity = first[kSecImportItemIdentity as String] as! SecIdentity
        var certificate: SecCertificate?
        XCTAssertEqual(SecIdentityCopyCertificate(identity, &certificate), errSecSuccess)
        return Identity(identity: identity, certificate: try XCTUnwrap(certificate))
    }

    /// Trusts exactly the test certificate, evaluated on a date inside its validity.
    private func trust(_ anchor: SecCertificate) -> ArchiveDownloader.ServerTrust {
        { trust, _ in
            SecTrustSetAnchorCertificates(trust, [anchor] as CFArray)
            SecTrustSetAnchorCertificatesOnly(trust, true)
            SecTrustSetVerifyDate(trust, Date(timeIntervalSince1970: 1_811_000_000) as CFDate) // 2027-05
            return SecTrustEvaluateWithError(trust, nil)
        }
    }

    // MARK: Servers

    /// A request a server received: its path and its headers, names in lower case.
    private struct Received {
        let path: String
        let headers: [String: String]
    }

    /// One HTTP/1.1 server on 127.0.0.1, TLS or plain, that answers one request per connection
    /// from the fixture's routes and records what it received.
    private final class Server: @unchecked Sendable {
        let listener: NWListener
        private let queue = DispatchQueue(label: "ext.download-test.server")
        private let lock = NSLock()
        private var log: [Received] = []
        private var connections = 0
        var routes: [String: [String: Any]] = [:]
        var substitute: (String) -> String = { $0 }

        init(tls: SecIdentity?) throws {
            let parameters: NWParameters
            if let tls {
                let options = NWProtocolTLS.Options()
                sec_protocol_options_set_local_identity(options.securityProtocolOptions, sec_identity_create(tls)!)
                parameters = NWParameters(tls: options)
            } else {
                parameters = .tcp
            }
            parameters.requiredLocalEndpoint = NWEndpoint.hostPort(host: "127.0.0.1", port: .any)
            listener = try NWListener(using: parameters)
        }

        func start() throws -> UInt16 {
            let ready = DispatchSemaphore(value: 0)
            listener.stateUpdateHandler = { state in
                if case .ready = state { ready.signal() }
                if case .failed = state { ready.signal() }
            }
            listener.newConnectionHandler = { [weak self] connection in self?.accept(connection) }
            listener.start(queue: queue)
            guard ready.wait(timeout: .now() + 10) == .success, let port = listener.port?.rawValue else {
                throw XCTSkip("the test server did not start")
            }
            return port
        }

        func stop() { listener.cancel() }

        var received: [Received] { lock.withLock { log } }
        var connectionCount: Int { lock.withLock { connections } }
        func reset() { lock.withLock { log = []; connections = 0 } }

        private func accept(_ connection: NWConnection) {
            lock.withLock { connections += 1 }
            connection.start(queue: queue)
            read(connection, Data())
        }

        private func read(_ connection: NWConnection, _ buffer: Data) {
            connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, complete, error in
                guard let self else { return connection.cancel() }
                var buffer = buffer
                if let data { buffer.append(data) }
                if let end = buffer.range(of: Data("\r\n\r\n".utf8)) {
                    self.answer(connection, String(decoding: buffer[..<end.lowerBound], as: UTF8.self))
                } else if complete || error != nil {
                    connection.cancel()
                } else {
                    self.read(connection, buffer)
                }
            }
        }

        private func answer(_ connection: NWConnection, _ head: String) {
            let lines = head.components(separatedBy: "\r\n")
            let path = lines.first?.split(separator: " ").dropFirst().first.map(String.init) ?? ""
            var headers: [String: String] = [:]
            for line in lines.dropFirst() {
                guard let colon = line.firstIndex(of: ":") else { continue }
                headers[line[..<colon].lowercased()] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
            }
            lock.withLock { log.append(Received(path: path, headers: headers)) }

            var response: Data
            let route = routes[path]
            if let route, let redirect = route["redirect"] as? String {
                response = Data("HTTP/1.1 302 Found\r\nLocation: \(substitute(redirect))\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".utf8)
            } else if let route {
                let status = route["status"] as? Int ?? 200
                let body: Data
                if let data = route["data"] as? Data {
                    body = data
                } else if let count = route["bodyBytes"] as? Int {
                    body = Data(repeating: UInt8(ascii: "x"), count: count)
                } else {
                    body = Data((route["body"] as? String ?? "").utf8)
                }
                response = Data("HTTP/1.1 \(status) Status\r\nContent-Type: application/octet-stream\r\nConnection: close\r\n".utf8)
                if route["chunked"] as? Bool == true {
                    response.append(Data("Transfer-Encoding: chunked\r\n\r\n".utf8))
                    var offset = 0
                    while offset < body.count {
                        let chunk = body[offset..<min(offset + 512, body.count)]
                        response.append(Data((String(chunk.count, radix: 16) + "\r\n").utf8))
                        response.append(chunk)
                        response.append(Data("\r\n".utf8))
                        offset += chunk.count
                    }
                    response.append(Data("0\r\n\r\n".utf8))
                } else {
                    response.append(Data("Content-Length: \(body.count)\r\n\r\n".utf8))
                    response.append(body)
                }
            } else {
                response = Data("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".utf8)
            }
            connection.send(content: response, contentContext: .finalMessage, isComplete: true,
                            completion: .contentProcessed { _ in connection.cancel() })
        }
    }

    // MARK: Cases

    private func outcome(_ error: Error?) -> String {
        guard let error else { return "ok" }
        if case ArchiveDownloadError.invalid = error { return "invalid" }
        return "failed"
    }

    func testEveryDownloadCase() async throws {
        let file = Self.fixtures.appendingPathComponent("download-cases.json")
        guard FileManager.default.fileExists(atPath: file.path) else {
            throw XCTSkip("the shared fixtures are not here: \(file.path)")
        }
        let document = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any])
        XCTAssertEqual(document["maxRedirects"] as? Int, ArchiveDownloader.maxRedirects)
        let routes = try XCTUnwrap(document["routes"] as? [String: [String: Any]])
        let cases = try XCTUnwrap(document["cases"] as? [[String: Any]])
        XCTAssertEqual(cases.count, 22, "the fixture changed size; check this test still covers every field")

        let id = try identity()
        let origin = try Server(tls: id.identity)
        let plain = try Server(tls: nil)
        defer { origin.stop(); plain.stop() }
        let originHost = "127.0.0.1:\(try origin.start())"
        let plainHost = "127.0.0.1:\(try plain.start())"
        let substitute: (String) -> String = {
            $0.replacingOccurrences(of: "{origin}", with: originHost).replacingOccurrences(of: "{plain}", with: plainHost)
        }
        origin.routes = routes
        origin.substitute = substitute
        plain.routes = routes
        let downloader = ArchiveDownloader(serverTrust: trust(id.certificate))

        let base = FileManager.default.temporaryDirectory.appendingPathComponent("ext-download-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: base) }

        for testCase in cases {
            let name = try XCTUnwrap(testCase["name"] as? String)
            origin.reset()
            let destination = base.appendingPathComponent(name)
            let existing = Data("already here".utf8)
            if testCase["destinationExists"] as? Bool == true { try existing.write(to: destination) }
            let source = substitute(try XCTUnwrap(testCase["url"] as? String))
            let headers = testCase["headers"] as? [String: String] ?? [:]
            let maxBytes = (testCase["maxBytes"] as? NSNumber)?.uint64Value ?? 0
            let expect = try XCTUnwrap(testCase["expect"] as? String)

            var failure: Error?
            if let url = URL(string: source) {
                do {
                    try await downloader.download(url, to: destination, maxBytes: maxBytes, headers: headers)
                } catch {
                    failure = error
                }
            } else {
                failure = ArchiveDownloadError.invalid // a URL Foundation cannot parse is refused before any request
            }
            XCTAssertEqual(outcome(failure), expect, "\(name): \(String(describing: failure))")

            if testCase["destinationExists"] as? Bool == true {
                XCTAssertEqual(try? Data(contentsOf: destination), existing, "\(name): changed the file that existed")
            } else if expect == "ok" {
                XCTAssertEqual(try? String(contentsOf: destination, encoding: .utf8), testCase["body"] as? String, name)
            } else {
                XCTAssertFalse(FileManager.default.fileExists(atPath: destination.path), "\(name): left a file")
            }
            if let absent = testCase["absentAtLanding"] as? [String] {
                let landing = try XCTUnwrap(origin.received.last, name)
                for header in absent {
                    XCTAssertNil(landing.headers[header.lowercased()], "\(name): \(header) followed the redirect")
                }
                // The first request did carry them, so their absence is the redirect's doing.
                let first = try XCTUnwrap(origin.received.first, name)
                for header in absent { XCTAssertNotNil(first.headers[header.lowercased()], "\(name): \(header) not sent") }
            }
        }
        XCTAssertEqual(plain.connectionCount, 0, "the plain HTTP server was reached")
    }

    func testInstallDownloadsChecksAndInstallsAnArchive() async throws {
        let archives = Self.fixtures.appendingPathComponent("archives")
        guard FileManager.default.fileExists(atPath: archives.path) else { throw XCTSkip("the shared fixtures are not here") }
        let archive = try Data(contentsOf: archives.appendingPathComponent("tar-gz-top.bin"))
        let digest = "817dde24b2deeb6b0eab23a0fa3c6d4fd7cf09401297a00396cb34a24169f480"

        let id = try identity()
        let origin = try Server(tls: id.identity)
        defer { origin.stop() }
        let host = "127.0.0.1:\(try origin.start())"
        origin.routes = ["/archive": ["data": archive]]
        let downloader = ArchiveDownloader(serverTrust: trust(id.certificate))
        let url = try XCTUnwrap(URL(string: "https://\(host)/archive"))

        let base = FileManager.default.temporaryDirectory.appendingPathComponent("ext-download-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: base) }
        let store = try PackageStore(root: base.appendingPathComponent("store").path)
        do {
            try await downloader.install(url, sha256: String(repeating: "0", count: 64), into: store)
            XCTFail("a wrong digest installed")
        } catch {
            XCTAssertEqual(error as? ArchiveDownloadError, .store(.mismatch))
        }
        XCTAssertEqual(try store.ids(), [])
        try await downloader.install(url, sha256: digest, id: "example/archive", into: store)
        XCTAssertEqual(try store.receipt("example/archive").artifactSHA256, digest)

        let small = try PackageStore(root: base.appendingPathComponent("small").path,
                                     limits: PackageStoreLimits(maxArchiveBytes: 64))
        do {
            try await downloader.install(url, sha256: digest, into: small)
            XCTFail("an archive over the store's limit was downloaded")
        } catch {
            XCTAssertEqual(error as? ArchiveDownloadError, .invalid)
        }

        // Without the test's trust the self-signed origin is refused by the system.
        do {
            try await ArchiveDownloader().install(url, sha256: digest, into: store)
            XCTFail("an untrusted server was accepted")
        } catch {
            guard case ArchiveDownloadError.transport = error else { return XCTFail("\(error)") }
        }
    }
}
