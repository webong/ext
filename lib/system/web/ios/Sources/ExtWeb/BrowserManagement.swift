import Foundation
import Darwin

public enum ManagementError: Error { case invalidJSON, invalidRequest, invalidResponse, invalidEndpoint }

/// Portable contracts; the caller supplies native behavior through dispatch's backend closure.
public enum BrowserManagement {
    public static let version = "1.0"
    public static let maxRequestInputBytes = 8 << 20
    public static let maxResultBytes = 16 << 20
    public static let maxResponseBytes = maxResultBytes + (64 << 10)
    public static let maxEndpointBytes = 2048
    public static let maxEndpointResultBytes = 4096
    public enum Status {
        public static let ready = "ready"
        public static let prepared = "prepared"
        public static let packaged = "packaged"
        public static let staged = "staged"
        public static let signed = "signed"
        public static let installed = "installed"
        public static let activated = "activated"
        public static let updated = "updated"
        public static let uninstalled = "uninstalled"
        public static let requested = "requested"
        public static let requestRemoved = "request-removed"
        public static let policyUpdated = "policy-updated"
        public static let awaitingBrowserAction = "awaiting-browser-action"
        public static let awaitingBrowserConfirmation = "awaiting-browser-confirmation"
        public static let reconciled = "reconciled"
        public static let connected = "connected"
        public static let navigated = "navigated"
        public static let injected = "injected"
    }
    public static func persistent(_ status: String) -> Bool { status == Status.installed }
    public static var operations: [String: [String]] { [
        "extension": ["activate", "capabilities", "convert", "install", "package", "policy", "prepare", "sign", "stage", "store_install", "store_remove", "targets", "update_manifest"],
        "userscript": ["activate", "describe", "disable", "enable", "install", "list", "prepare", "uninstall", "update"],
        "session": ["connect", "endpoint", "inject", "navigate", "replay", "targets"],
        "bookmarklet": ["decode", "encode", "install_page"]
    ] }
    private static func quote(_ value: String) -> String {
        // Encoding a string cannot fail.
        String(decoding: try! JSONEncoder().encode(value), as: UTF8.self)
    }
    private static func field(_ m: [String: String], _ key: String) throws -> String { try ManagementJSON.string(m[key]) }
    /// Input/result are raw JSON, preserving numbers and whitespace. nil means absent.
    public struct Request: CustomStringConvertible, CustomDebugStringConvertible {
        public let version: String
        public let kind: String
        public let action: String
        public let input: String?
        public init(version: String = BrowserManagement.version, kind: String, action: String, input: String? = nil) {
            self.version = version; self.kind = kind; self.action = action; self.input = input
        }
        public func validate() throws {
            guard version == BrowserManagement.version, operations[kind]?.contains(action) == true,
                  (input?.utf8.count ?? 0) <= maxRequestInputBytes else { throw ManagementError.invalidRequest }
            if let input, !input.isEmpty { _ = try ManagementJSON.read(input, object: true) }
        }
        public func json() throws -> String {
            try validate()
            return "{\"version\":\(quote(version)),\"kind\":\(quote(kind)),\"action\":\(quote(action))" +
                (input == nil || input!.isEmpty ? "" : ",\"input\":\(input!)") + "}"
        }
        public static func decode(_ json: String) throws -> Self {
            guard json.utf8.count <= maxRequestInputBytes + (64 << 10) else { throw ManagementError.invalidRequest }
            let m = try ManagementJSON.read(json, object: true)
            let r = try Self(version: field(m, "version"), kind: field(m, "kind"), action: field(m, "action"), input: m["input"])
            try r.validate(); return r
        }
        public var description: String { "BrowserManagement.Request(<redacted>)" }
        public var debugDescription: String { description }
    }
    public struct Response: CustomStringConvertible, CustomDebugStringConvertible {
        public let version: String
        public let kind: String
        public let action: String
        public let status: String
        public let result: String?
        public init(version: String = BrowserManagement.version, kind: String, action: String, status: String, result: String? = nil) {
            self.version = version; self.kind = kind; self.action = action; self.status = status; self.result = result
        }
        public init(request: Request, status: String, result: String? = nil) {
            self.init(kind: request.kind, action: request.action, status: status, result: result)
        }
        public func validate(for request: Request) throws {
            guard version == BrowserManagement.version, kind == request.kind, action == request.action,
                  !status.isEmpty, status.utf8.count <= 64, (result?.utf8.count ?? 0) <= maxResultBytes else { throw ManagementError.invalidResponse }
            if let result, !result.isEmpty { _ = try ManagementJSON.read(result) }
            if kind == "session" && action == "endpoint" {
                guard let result, result.utf8.count <= maxEndpointResultBytes else { throw ManagementError.invalidEndpoint }
                _ = try Endpoint.decode(result)
            }
            if kind == "extension" && ((action == "activate" && status == Status.installed) ||
                (action == "install" && status == Status.activated)) { throw ManagementError.invalidResponse }
        }
        public func json(for request: Request) throws -> String {
            try validate(for: request)
            let json = "{\"version\":\(quote(version)),\"kind\":\(quote(kind)),\"action\":\(quote(action)),\"status\":\(quote(status))" +
                (result == nil || result!.isEmpty ? "" : ",\"result\":\(result!)") + "}"
            guard json.utf8.count <= maxResponseBytes else { throw ManagementError.invalidResponse }; return json
        }
        public static func decode(_ json: String, for request: Request) throws -> Self {
            guard json.utf8.count <= maxResponseBytes else { throw ManagementError.invalidResponse }
            let m = try ManagementJSON.read(json, object: true)
            let r = try Self(version: field(m, "version"), kind: field(m, "kind"), action: field(m, "action"), status: field(m, "status"), result: m["result"])
            try r.validate(for: request); return r
        }
        public var description: String { "BrowserManagement.Response(<redacted>)" }
        public var debugDescription: String { description }
    }
    public struct Endpoint: CustomStringConvertible, CustomDebugStringConvertible {
        public let `protocol`: String
        public let endpoint: String
        public init(protocol: String, endpoint: String) { self.protocol = `protocol`; self.endpoint = endpoint }
        public func validate() throws {
            guard ["cdp", "bidi"].contains(`protocol`), !endpoint.isEmpty, endpoint.utf8.count <= maxEndpointBytes,
                  endpoint.hasPrefix("ws://") || endpoint.hasPrefix("wss://"),
                  !endpoint.contains("?"), !endpoint.contains("#") else { throw ManagementError.invalidEndpoint }
            let bytes = Array(endpoint.utf8)
            for i in bytes.indices {
                if bytes[i] < 32 || bytes[i] == 127 { throw ManagementError.invalidEndpoint }
                if bytes[i] == 37 {
                    guard i + 2 < bytes.count,
                          bytes[(i + 1)...(i + 2)].allSatisfy({ Array("0123456789abcdefABCDEF".utf8).contains($0) }) else { throw ManagementError.invalidEndpoint }
                }
            }
            let authority = endpoint.components(separatedBy: "://").dropFirst().joined(separator: "://").split(separator: "/", omittingEmptySubsequences: false).first ?? ""
            guard let colon = authority.lastIndex(of: ":") else { throw ManagementError.invalidEndpoint }
            let port = authority[authority.index(after: colon)...]
            guard !port.isEmpty, port.utf8.allSatisfy({ $0 >= 48 && $0 <= 57 }) else { throw ManagementError.invalidEndpoint }
            var host = String(authority[..<colon])
            if host.hasPrefix("[") && host.hasSuffix("]") {
                host = String(host.dropFirst().dropLast())
                guard host.contains(":") else { throw ManagementError.invalidEndpoint }
            }
            var loopback = host.lowercased() == "localhost"
            if IPLiteral.isIP(host) {
                var v4 = in_addr()
                var v6 = in6_addr()
                if inet_pton(AF_INET, host, &v4) == 1 { loopback = withUnsafeBytes(of: v4) { $0[0] == 127 } }
                else if inet_pton(AF_INET6, host, &v6) == 1 {
                    loopback = withUnsafeBytes(of: v6) { b in
                        (b.prefix(15).allSatisfy { $0 == 0 } && b[15] == 1) ||
                        (b.prefix(10).allSatisfy { $0 == 0 } && b[10] == 255 && b[11] == 255 && b[12] == 127)
                    }
                }
            }
            guard loopback else { throw ManagementError.invalidEndpoint }
        }
        public func json() throws -> String {
            try validate(); return "{\"protocol\":\(quote(`protocol`)),\"endpoint\":\(quote(endpoint))}"
        }
        public static func decode(_ json: String) throws -> Self {
            guard json.utf8.count <= maxEndpointResultBytes else { throw ManagementError.invalidEndpoint }
            let m = try ManagementJSON.read(json, object: true)
            guard m.keys.allSatisfy({ ["protocol", "endpoint"].contains($0) }) else { throw ManagementError.invalidEndpoint }
            let e = try Self(protocol: field(m, "protocol"), endpoint: field(m, "endpoint")); try e.validate(); return e
        }
        public var description: String { "BrowserManagement.Endpoint(<redacted>)" }
        public var debugDescription: String { description }
    }
    /// Validates before invoking the caller's backend; errors propagate to the caller.
    public static func dispatch(profile: String, request: Request, backend: (String, Request) throws -> Response) throws -> Response {
        try request.validate()
        let response = try backend(profile, request)
        try response.validate(for: request); return response
    }
    /// Async variant for native workspaces. The backend owns execution/actor affinity and cancellation.
    public static func dispatch(profile: String, request: Request, backend: (String, Request) async throws -> Response) async throws -> Response {
        try request.validate()
        let response = try await backend(profile, request)
        try response.validate(for: request); return response
    }
}
