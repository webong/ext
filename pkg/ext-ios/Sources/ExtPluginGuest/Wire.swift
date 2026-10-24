import Foundation

/// The ext.plugin/v1 wire types an author needs. The engine validates every
/// request against the descriptor, so a handler sees only well-formed calls.
public let apiVersion = "ext.plugin/v1"

public struct Identity: Codable, Equatable, Sendable {
    public var id: String
    public var revision: String
    public var version: String?
    public init(id: String, revision: String, version: String? = nil) {
        self.id = id
        self.revision = revision
        self.version = version
    }
}

public struct ContractRef: Codable, Equatable, Sendable {
    public var name: String
    public var version: String
    public init(name: String, version: String) {
        self.name = name
        self.version = version
    }
}

public struct Operation: Codable, Equatable, Sendable {
    public var name: String
    public var surface: String?
    public init(name: String, surface: String? = nil) {
        self.name = name
        self.surface = surface
    }
}

public struct Contract: Codable, Equatable, Sendable {
    public var name: String
    public var version: String
    public var operations: [Operation]
    public init(name: String, version: String, operations: [Operation]) {
        self.name = name
        self.version = version
        self.operations = operations
    }
}

public struct Descriptor: Codable, Equatable, Sendable {
    public var apiVersion: String
    public var identity: Identity
    public var contracts: [Contract]
    public init(identity: Identity, contracts: [Contract]) {
        self.apiVersion = ExtPluginGuest.apiVersion
        self.identity = identity
        self.contracts = contracts
    }
}

/// A public, caller-visible failure. Any other error a handler throws is private:
/// the engine reduces it to a generic operation_failed response.
public struct RemoteError: Error, Codable, Equatable, Sendable {
    public var code: String
    public var message: String
    public var retryAfterMilliseconds: Int64?
    public init(code: String, message: String, retryAfterMilliseconds: Int64? = nil) {
        self.code = code
        self.message = message
        self.retryAfterMilliseconds = retryAfterMilliseconds
    }
}

/// Thrown by `Call.checkDeadline()` once the call's time is spent.
public struct DeadlineExceeded: Error, Sendable {}

/// One admitted invocation.
public struct Call: Sendable {
    public let id: String
    public let plugin: Identity
    public let contract: ContractRef
    public let operation: String
    public let surface: String?
    /// The request payload exactly as the caller sent it: one JSON value, or
    /// empty when the request had none. Numbers keep their original text.
    public let payload: Data
    let expiry: DispatchTime

    public var remainingMilliseconds: UInt32 {
        let now = DispatchTime.now().uptimeNanoseconds
        return expiry.uptimeNanoseconds > now ? UInt32(min((expiry.uptimeNanoseconds - now) / 1_000_000, UInt64(UInt32.max))) : 0
    }

    /// Long-running handlers call this to honour the deadline; native work cannot
    /// be interrupted from outside.
    public func checkDeadline() throws {
        if remainingMilliseconds == 0 { throw DeadlineExceeded() }
    }

    public func decodePayload<T: Decodable>(_ type: T.Type = T.self) throws -> T {
        try JSONDecoder().decode(type, from: payload.isEmpty ? Data("null".utf8) : payload)
    }
}

struct Envelope: Decodable {
    var id: String
    var plugin: Identity
    var contract: ContractRef
    var operation: String
    var surface: String?
}

/// Returns the bytes of the top-level "payload" value of an already validated
/// request, without re-encoding it. Empty when the key is absent.
func rawPayload(in request: [UInt8]) -> [UInt8] {
    var i = 0
    func skipSpace() { while i < request.count, [0x20, 0x09, 0x0A, 0x0D].contains(request[i]) { i += 1 } }
    func skipString() { // at the opening quote
        i += 1
        while i < request.count {
            if request[i] == 0x5C { i += 2; continue }
            if request[i] == 0x22 { i += 1; return }
            i += 1
        }
    }
    func skipValue() {
        skipSpace()
        guard i < request.count else { return }
        switch request[i] {
        case 0x22: skipString()
        case 0x7B, 0x5B:
            var depth = 0
            while i < request.count {
                switch request[i] {
                case 0x22: skipString(); continue
                case 0x7B, 0x5B: depth += 1
                case 0x7D, 0x5D: depth -= 1
                default: break
                }
                i += 1
                if depth == 0 { return }
            }
        default:
            while i < request.count, ![0x2C, 0x7D, 0x5D, 0x20, 0x09, 0x0A, 0x0D].contains(request[i]) { i += 1 }
        }
    }
    skipSpace()
    guard i < request.count, request[i] == 0x7B else { return [] }
    i += 1
    while true {
        skipSpace()
        guard i < request.count, request[i] == 0x22 else { return [] }
        let keyStart = i
        skipString()
        let key = Array(request[keyStart..<i])
        skipSpace()
        guard i < request.count, request[i] == 0x3A else { return [] }
        i += 1
        skipSpace()
        let valueStart = i
        skipValue()
        if key == Array("\"payload\"".utf8) { return Array(request[valueStart..<i]) }
        skipSpace()
        guard i < request.count, request[i] == 0x2C else { return [] }
        i += 1
    }
}
