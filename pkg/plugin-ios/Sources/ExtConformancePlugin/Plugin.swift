import ExtPluginExports
import ExtPluginGuest
import Foundation

// The ext.conformance/v1 fixture every language implements, so one Go suite can
// check a Swift-built dynamic library through the shared cshared ABI.

struct PrivateFailure: Error {}

let conformanceDescriptor = Descriptor(
    identity: Identity(id: "ctx/conformance", revision: "fixture-1"),
    contracts: [Contract(
        name: "ext.conformance", version: "v1",
        operations: ["echo", "wait", "private-error", "public-error"].map { Operation(name: $0) })])

func conformanceHandle(_ call: Call) throws -> Data {
    switch call.operation {
    case "wait":
        while true {
            try call.checkDeadline()
            usleep(1000)
        }
    case "private-error":
        throw PrivateFailure()
    case "public-error":
        throw RemoteError(code: "busy", message: "try later", retryAfterMilliseconds: 10)
    default:
        return call.payload
    }
}

@_cdecl("ext_plugin_guest_factory")
public func ext_plugin_guest_factory() -> UnsafeMutableRawPointer? {
    exportGuest { try Guest(descriptor: conformanceDescriptor, handler: conformanceHandle) }
}
