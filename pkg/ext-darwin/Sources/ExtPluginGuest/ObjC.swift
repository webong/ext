import Foundation

// Objective-C access to the guest SDK. Swift's `throws` becomes `NSError **`, and the
// classes carry the EXT prefix Objective-C expects. A handler returns the payload
// JSON, or returns nil with an error: an NSError made by
// +[EXTGuest remoteErrorWithCode:message:retryAfterMilliseconds:] is public, and any
// other error is private and reduced to a generic operation_failed.

public let EXTRemoteErrorDomain = "EXTRemoteErrorDomain"

/// One admitted invocation.
@objc(EXTCall)
public final class ObjCCall: NSObject {
    @objc public let identifier: String
    @objc public let operation: String
    @objc public let contractName: String
    @objc public let contractVersion: String
    @objc public let pluginIdentifier: String
    /// The admission surface, or nil.
    @objc public let surface: String?
    /// The request payload exactly as sent: one JSON value, or empty when absent.
    @objc public let payload: Data
    private let call: Call

    init(_ call: Call) {
        self.call = call
        identifier = call.id
        operation = call.operation
        contractName = call.contract.name
        contractVersion = call.contract.version
        pluginIdentifier = call.plugin.id
        surface = call.surface
        payload = call.payload
    }

    @objc public var remainingMilliseconds: UInt32 { call.remainingMilliseconds }

    /// YES once the call's time is spent. Long handlers check it in loops.
    @objc public var deadlineExceeded: Bool { call.remainingMilliseconds == 0 }
}

/// A plugin guest authored from Objective-C.
@objc(EXTGuest)
public final class ObjCGuest: NSObject {
    /// The Swift guest, for the C ABI export hook in ExtPluginExports.
    public let guest: Guest

    /// - Parameters:
    ///   - operations: operation names of the one contract this guest serves.
    ///   - handler: runs on the calling thread. Return the payload JSON; return nil with
    ///     *error set to fail. It must not raise an exception or trap.
    @objc public init(
        identifier: String, revision: String, contractName: String, contractVersion: String,
        operations: [String], handler: @escaping (ObjCCall, NSErrorPointer) -> Data?
    ) throws {
        let descriptor = Descriptor(
            identity: Identity(id: identifier, revision: revision),
            contracts: [Contract(name: contractName, version: contractVersion, operations: operations.map { Operation(name: $0) })])
        guest = try Guest(descriptor: descriptor) { call in
            var error: NSError?
            if let result = handler(ObjCCall(call), &error) { return result }
            if let error, error.domain == EXTRemoteErrorDomain {
                throw RemoteError(
                    code: (error.userInfo["code"] as? String) ?? "operation_failed",
                    message: (error.userInfo["message"] as? String) ?? "",
                    retryAfterMilliseconds: (error.userInfo["retryAfterMilliseconds"] as? NSNumber)?.int64Value)
            }
            throw error ?? NSError(domain: "EXTGuest", code: 1)
        }
        super.init()
    }

    /// A public error for a handler to return through its error argument.
    @objc public static func remoteError(code: String, message: String, retryAfterMilliseconds: Int64) -> NSError {
        NSError(domain: EXTRemoteErrorDomain, code: 1, userInfo: [
            "code": code, "message": message, "retryAfterMilliseconds": NSNumber(value: retryAfterMilliseconds),
        ])
    }

    /// The descriptor exactly as the engine serves it to a handshake.
    @objc public func descriptorJSON() throws -> Data { try guest.descriptorJSON() }

    /// Validates a complete ext.plugin/v1 request and returns the complete response.
    @objc public func invoke(_ request: Data, timeoutMilliseconds: UInt32) throws -> Data {
        try guest.invoke(request, timeoutMilliseconds: timeoutMilliseconds)
    }
}
