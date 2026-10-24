import CExtEngine
import CExtPluginShim
import ExtPluginGuest
import Foundation

// The four C-ABI entry points of the ext.plugin cshared ABI v1, over Guest.
// A plugin is a dynamic library that links this target and defines one
// ext_plugin_guest_factory; see exportGuest.

private final class Session {
    let guest: Guest
    var handshaken = false
    let lock = NSLock()
    init(_ guest: Guest) { self.guest = guest }
}

private let registryLock = NSLock()
private var sessions: [UInt64: Session] = [:]
private var lastHandle: UInt64 = 0
private let maxSessions = 64
/// EXT_PLUGIN_MAX_FRAME_BYTES; Swift cannot import an expression macro.
public let extPluginMaxFrameBytes: UInt32 = 24 * 1024 * 1024
private let maxFrame = extPluginMaxFrameBytes

/// Builds a guest for ext_plugin_open and hands it to the exports.
/// Use it as the body of the one function a plugin defines:
///
///     @_cdecl("ext_plugin_guest_factory")
///     public func ext_plugin_guest_factory() -> UnsafeMutableRawPointer? {
///         exportGuest { try Guest(descriptor: descriptor, handler: handle) }
///     }
public func exportGuest(_ make: () throws -> Guest) -> UnsafeMutableRawPointer? {
    guard let guest = try? make() else { return nil }
    return Unmanaged.passRetained(guest).toOpaque()
}

/// Objective-C hook for the one function a plugin defines. From Objective-C:
///
///     void *ext_plugin_guest_factory(void) {
///         return [EXTPluginExport retainedPointerFor:makeGuest()];
///     }
@objc(EXTPluginExport)
public final class ObjCPluginExport: NSObject {
    /// A retained pointer for ext_plugin_guest_factory, or NULL when `guest` is nil.
    @objc public static func retainedPointer(for guest: ObjCGuest?) -> UnsafeMutableRawPointer? {
        guard let guest else { return nil }
        return Unmanaged.passRetained(guest.guest).toOpaque()
    }
}

@_cdecl("ext_plugin_abi_version")
public func extPluginABIVersion() -> UInt32 { UInt32(EXT_PLUGIN_ABI_VERSION) }

@_cdecl("ext_plugin_open")
public func extPluginOpen() -> UInt64 {
    registryLock.lock()
    defer { registryLock.unlock() }
    guard sessions.count < maxSessions, lastHandle < UInt64.max,
          let raw = ext_plugin_guest_factory() else { return 0 }
    lastHandle += 1
    sessions[lastHandle] = Session(Unmanaged<Guest>.fromOpaque(raw).takeRetainedValue())
    return lastHandle
}

@_cdecl("ext_plugin_close")
public func extPluginClose(_ handle: UInt64) {
    registryLock.lock()
    let session = sessions.removeValue(forKey: handle)
    registryLock.unlock()
    // Wait for an outstanding call on this handle before releasing the guest.
    session?.lock.lock()
    session?.lock.unlock()
}

@_cdecl("ext_plugin_call")
public func extPluginCall(
    _ handle: UInt64, _ operation: UInt32,
    _ request: UnsafePointer<UInt8>?, _ requestLength: UInt32,
    _ response: UnsafeMutablePointer<UInt8>?, _ capacity: UInt32,
    _ written: UnsafeMutablePointer<UInt32>?
) -> UInt32 {
    guard let written else { return UInt32(EXT_PLUGIN_INVALID) }
    written.pointee = 0
    guard let request, let response, requestLength > 0, requestLength <= maxFrame, capacity == maxFrame else {
        return UInt32(EXT_PLUGIN_INVALID)
    }
    registryLock.lock()
    let session = sessions[handle]
    registryLock.unlock()
    guard let session else { return UInt32(EXT_PLUGIN_CLOSED) }
    session.lock.lock()
    defer { session.lock.unlock() }
    let input = Data(bytes: request, count: Int(requestLength))
    let output: Data
    do {
        switch operation {
        case UInt32(EXT_PLUGIN_HANDSHAKE):
            try checkHandshake(input)
            session.handshaken = true
            output = try session.guest.descriptorJSON()
        case UInt32(EXT_PLUGIN_INVOKE):
            guard session.handshaken else { return UInt32(EXT_PLUGIN_FAILED) }
            output = try session.guest.invoke(input)
        default:
            return UInt32(EXT_PLUGIN_FAILED)
        }
    } catch {
        return UInt32(EXT_PLUGIN_FAILED)
    }
    guard output.count <= Int(capacity) else { return UInt32(EXT_PLUGIN_INVALID) }
    output.copyBytes(to: response, count: output.count)
    written.pointee = UInt32(output.count)
    return UInt32(EXT_PLUGIN_OK)
}

private struct HandshakeFailure: Error {}

/// A handshake is {"deadline":"RFC3339 timestamp"} and must not have expired.
private func checkHandshake(_ input: Data) throws {
    guard let object = try JSONSerialization.jsonObject(with: input) as? [String: Any],
          object.count == 1, let text = object["deadline"] as? String,
          let deadline = parseRFC3339(text), deadline > Date() else { throw HandshakeFailure() }
}

private func parseRFC3339(_ text: String) -> Date? {
    let fractional = ISO8601DateFormatter()
    fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let date = fractional.date(from: text) { return date }
    let plain = ISO8601DateFormatter()
    plain.formatOptions = [.withInternetDateTime]
    return plain.date(from: text)
}
