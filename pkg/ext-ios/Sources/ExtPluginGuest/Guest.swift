import CExtEngine
import Foundation

/// Runs one admitted call and returns its result payload as one JSON value.
/// Throw `RemoteError` for a failure the caller may see; any other error is
/// private and is reduced to a generic operation_failed response.
///
/// A handler must not trap (`fatalError`, array bounds, forced unwraps): the
/// engine cannot recover a crashed process. It must cooperate with deadlines by
/// calling `Call.checkDeadline()` in long loops.
public typealias Handler = @Sendable (Call) throws -> Data

public enum GuestError: Error, Equatable {
    /// The engine rejected the operation with this ext_status value.
    case engine(Int32)
    case invalidDescriptor
}

/// A plugin guest. The C engine does the strict JSON parsing, request
/// validation against the descriptor, deadlines and error sanitizing.
public final class Guest: @unchecked Sendable {
    static let defaultMaxCallMilliseconds: UInt32 = 30_000

    private var engine: OpaquePointer?
    private let handler: Handler
    public let descriptor: Descriptor
    /// What the descriptor the engine accepted declares about progress and reverse calls.
    private(set) var declared = Declarations()

    public convenience init(descriptor: Descriptor, maxCallMilliseconds: UInt32 = 30_000, handler: @escaping Handler) throws {
        let bytes: Data
        do { bytes = try JSONEncoder().encode(descriptor) } catch { throw GuestError.invalidDescriptor }
        try self.init(descriptor: descriptor, descriptorJSON: bytes, maxCallMilliseconds: maxCallMilliseconds, handler: handler)
    }

    /// A guest over a descriptor in JSON, which the engine validates; for tests of the wire model.
    convenience init(descriptorJSON: Data, handler: @escaping Handler) throws {
        let decoded = (try? JSONDecoder().decode(Descriptor.self, from: descriptorJSON))
            ?? Descriptor(identity: Identity(id: "invalid", revision: "invalid"), contracts: [])
        try self.init(descriptor: decoded, descriptorJSON: descriptorJSON, maxCallMilliseconds: Guest.defaultMaxCallMilliseconds,
                      handler: handler)
    }

    private init(descriptor: Descriptor, descriptorJSON bytes: Data, maxCallMilliseconds: UInt32, handler: @escaping Handler) throws {
        self.descriptor = descriptor
        self.handler = handler
        var created: OpaquePointer?
        let status: ext_status = bytes.withUnsafeBytes { raw in
            var options = ext_guest_options(
                abi_version: UInt32(EXT_HOST_ABI_VERSION),
                struct_size: UInt32(MemoryLayout<ext_guest_options>.size),
                descriptor: raw.bindMemory(to: UInt8.self).baseAddress,
                descriptor_len: raw.count,
                max_call_ms: maxCallMilliseconds,
                user: Unmanaged.passUnretained(self).toOpaque(),
                handle: guestTrampoline)
            return ext_guest_create(&options, &created)
        }
        guard status == ext_status(EXT_OK), created != nil else { throw GuestError.engine(status) }
        engine = created
        do {
            // What the engine accepted is what a call is judged by.
            declared = try Declarations(descriptorJSON: Array(try descriptorJSON()))
        } catch {
            ext_guest_destroy(created)
            engine = nil
            throw error
        }
    }

    deinit {
        if let engine { ext_guest_destroy(engine) }
    }

    /// The descriptor exactly as the engine will serve it to a handshake.
    public func descriptorJSON() throws -> Data {
        var out = ext_buffer(data: nil, len: 0)
        defer { ext_buffer_free(&out) }
        let status = ext_guest_descriptor(engine, &out)
        guard status == ext_status(EXT_OK), let data = out.data else { throw GuestError.engine(status) }
        return Data(bytes: data, count: out.len)
    }

    /// Validates a complete ext.plugin/v1 request and returns the complete
    /// response, including public and sanitized private errors.
    public func invoke(_ request: Data, timeoutMilliseconds: UInt32 = 30_000) throws -> Data {
        try invoke(request, timeoutMilliseconds: timeoutMilliseconds, connection: nil)
    }

    /// As `invoke(_:timeoutMilliseconds:)`, on a connection that carries frames, if there is one.
    func invoke(_ request: Data, timeoutMilliseconds: UInt32, connection: Connection?) throws -> Data {
        var out = ext_buffer(data: nil, len: 0)
        defer { ext_buffer_free(&out) }
        let state = connection.map { Unmanaged.passUnretained($0).toOpaque() }
        let status: ext_status = request.withUnsafeBytes { raw in
            ext_guest_invoke(engine, raw.bindMemory(to: UInt8.self).baseAddress, raw.count, timeoutMilliseconds, state, &out)
        }
        guard status == ext_status(EXT_OK), let data = out.data else { throw GuestError.engine(status) }
        return Data(bytes: data, count: out.len)
    }

    fileprivate func run(request: UnsafeBufferPointer<UInt8>, remaining: UInt32, callUser: UnsafeMutableRawPointer?,
                         emit: ext_guest_emit, sink: UnsafeMutableRawPointer?) -> ext_status {
        let bytes = Array(request)
        guard let envelope = try? JSONDecoder().decode(Envelope.self, from: Data(bytes)) else {
            return ext_status(EXT_INVALID)
        }
        var channel: CallChannel?
        if let callUser {
            // A call gets a reporter when its operation declared progress and the host asked for
            // events. A request the call makes of the host is bound by the call's own deadline.
            let connection = Unmanaged<Connection>.fromOpaque(callUser).takeUnretainedValue()
            guard let object = (try? JSONValue.parse(bytes))?.object, let deadline = try? Frames.instant(object["deadline"]) else {
                return ext_status(EXT_INVALID)
            }
            let rate = object["progress"]?.object
            let limits = rate != nil && declared.progress(envelope.contract, envelope.operation) ? Frames.resolve(rate) : nil
            channel = CallChannel(connection: connection, id: envelope.id, deadline: deadline, limits: limits)
        }
        let call = Call(
            id: envelope.id, plugin: envelope.plugin, contract: envelope.contract,
            operation: envelope.operation, surface: envelope.surface,
            payload: Data(rawPayload(in: bytes)),
            expiry: DispatchTime.now() + .milliseconds(Int(remaining)), channel: channel)
        let result: Result<Data, Error> = Result { try handler(call) }
        // Before the response: nothing may follow it.
        channel?.finish()
        switch result {
        case .success(let payload):
            return Guest.emit(emit, sink, UInt32(EXT_GUEST_PAYLOAD), payload.isEmpty ? Data("null".utf8) : payload)
        case .failure(let error as RemoteError):
            guard let encoded = try? JSONEncoder().encode(error) else { return ext_status(EXT_IO) }
            return Guest.emit(emit, sink, UInt32(EXT_GUEST_PUBLIC_ERROR), encoded)
        case .failure:
            return ext_status(EXT_IO)
        }
    }

    private static func emit(_ emit: ext_guest_emit, _ sink: UnsafeMutableRawPointer?, _ kind: UInt32, _ data: Data) -> ext_status {
        data.withUnsafeBytes { raw in emit(sink, kind, raw.bindMemory(to: UInt8.self).baseAddress, raw.count) }
    }
}

private let guestTrampoline: @convention(c) (
    UnsafeMutableRawPointer?, UnsafeMutableRawPointer?, UnsafePointer<UInt8>?, Int, UInt32,
    ext_guest_emit?, UnsafeMutableRawPointer?
) -> ext_status = { user, callUser, request, length, remaining, emit, sink in
    guard let user, let request, let emit else { return ext_status(EXT_INVALID) }
    let guest = Unmanaged<Guest>.fromOpaque(user).takeUnretainedValue()
    return guest.run(request: UnsafeBufferPointer(start: request, count: length), remaining: remaining, callUser: callUser,
                     emit: emit, sink: sink)
}
