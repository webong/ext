import CExtWamr
import Foundation

/// Hosts an ext.io.pc/v1 WebAssembly reactor (docs/plugin-reactor-abi.md) in this process, on
/// the WAMR interpreter, through the shared engine's host. This is the fallback route of
/// docs/adr-mobile-wasm.md, for hosts where a web view is unsuitable: it needs no GUI, gives the
/// guest only the ABI's WASI imports, and enforces a deadline, a memory cap and an optional
/// instruction budget. It runs no JIT or ahead-of-time code, which iOS forbids.
///
/// The host takes the descriptor it expects and refuses a module whose handshake differs, so a
/// host chooses what it runs. Calls block the calling thread, so call from a background queue.
/// A guest that traps, overruns its deadline, runs out of memory or exits ends the whole
/// instance, and the host is then closed.
public final class WamrPluginHost: @unchecked Sendable {
    /// Why the engine refused an operation: its ext_plugin_status value and name.
    public struct Failure: Error, Equatable, CustomStringConvertible {
        public let status: Int32
        public let reason: String
        public var description: String { "ext_plugin_status \(status) (\(reason))" }
    }

    /// Limits and diagnostics for one instance. Zero means the engine's default.
    public struct Options: Sendable {
        /// Guest linear memory cap in 64 KiB pages; zero is 4096 (256 MiB).
        public var memoryLimitPages: UInt32 = 0
        /// Bytes of interpreter stack; zero is 1 MiB.
        public var stackSize: UInt32 = 0
        /// A budget of WebAssembly instructions per call, on top of the deadline; zero is none.
        public var instructionLimit: UInt64 = 0
        /// Bytes of guest output passed to `diagnostic` over the instance's life; zero is 65536.
        public var maxDiagnosticBytes: UInt32 = 0
        public init() {}
    }

    /// What a reverse hook answers: a JSON payload (empty is null), or a public error, the JSON
    /// of a RemoteError the guest receives as it is. Throwing reaches the guest as operation_failed.
    public enum ReverseReply: Sendable {
        case payload(Data)
        case publicError(Data)
    }

    private let context: Context
    private var host: OpaquePointer?
    private let lock = NSLock()
    private var closed = false
    /// The registry this host is a source of, and those it serves contracts in: kept alive while
    /// the host is, and left before it is destroyed.
    private var sourceOf: WamrRegistry?
    private var registered: [(registry: WamrRegistry, name: String, version: String)] = []

    /// - Parameters:
    ///   - module: the reactor module's bytes.
    ///   - descriptor: the descriptor JSON the host expects; the handshake must match it exactly.
    ///     `descriptor(of:)` returns a module's own, for inspection and tests.
    ///   - verify: runs before the module starts, with the descriptor; return false to refuse.
    ///   - authorize: runs on every validated request, before dispatch; return false to deny it.
    ///     A denial leaves the host usable. Both default to allowing.
    ///   - diagnostic: receives the guest's stdout and stderr, bounded by `Options.maxDiagnosticBytes`.
    public init(
        module: Data, descriptor: Data, options: Options = Options(),
        verify: ((Data) -> Bool)? = nil, authorize: ((Data) -> Bool)? = nil,
        diagnostic: ((String) -> Void)? = nil
    ) throws {
        context = Context(verify: verify, authorize: authorize, diagnostic: diagnostic)
        var backend = ext_plugin_backend_extension_context()
        try Self.makeBackend(module: module, options: options, context: context, into: &backend)
        let user = Unmanaged.passUnretained(context).toOpaque()
        var created: OpaquePointer?
        let status: ext_plugin_status = descriptor.withUnsafeBytes { raw in
            var hostOptions = ext_plugin_host_options(
                abi_version: UInt32(EXT_PLUGIN_HOST_ABI_VERSION),
                struct_size: UInt32(MemoryLayout<ext_plugin_host_options>.size),
                descriptor: raw.bindMemory(to: UInt8.self).baseAddress, descriptor_len: raw.count,
                verify: wamrVerify, authorize: wamrAuthorize, user: user)
            return withUnsafePointer(to: &backend) { pointer in
                var selected = ext_plugin_backend_options(
                    struct_size: UInt32(MemoryLayout<ext_plugin_backend_options>.size),
                    kind: UInt32(EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT), config: UnsafeRawPointer(pointer),
                    config_size: MemoryLayout<ext_plugin_backend_extension_context>.size)
                return ext_plugin_host_create(&hostOptions, &selected, &created)
            }
        }
        guard status == ext_plugin_status(EXT_PLUGIN_OK), let created else {
            // The engine did not take ownership of the backend, so release it here.
            backend.release?(backend.user)
            throw Self.failure(status)
        }
        host = created
        // The guest may call its host (ext_plugin.reverse); serve(...) or attach(...) say how.
        let bound = ext_wamr_backend_bind_host(backend.user, created)
        if bound != ext_plugin_status(EXT_PLUGIN_OK) {
            ext_plugin_host_destroy(created)
            host = nil
            throw Self.failure(bound)
        }
    }

    deinit {
        close()
        lock.lock()
        defer { lock.unlock() }
        if let host {
            // Calls routed here have ended with the close; unregistering waits for them.
            for entry in registered { entry.registry.leave(name: entry.name, version: entry.version, host: self) }
            registered = []
            ext_plugin_host_destroy(host)
        }
        host = nil
    }

    /// Offers the guest the operations its descriptor lists under `reverse`, before `start`. The
    /// engine admits or refuses each request with a stable code; `authorize` and `handle` get the
    /// Request JSON and are the host's policy. They run on the thread inside `invoke` or `call`,
    /// must not use this host, and the call's deadline keeps running while they do.
    public func serve(authorize: @escaping (Data) -> Bool, handle: @escaping (Data) throws -> ReverseReply,
                      timeout: TimeInterval = 0, maxPayloadBytes: UInt32 = 0, maxInFlight: UInt32 = 0) throws {
        context.reverseAuthorize = authorize
        context.reverseHandle = handle
        var services = services(timeout: timeout, maxPayloadBytes: maxPayloadBytes, maxInFlight: maxInFlight, handle: true)
        let status = ext_plugin_host_set_services(try live(), &services)
        if status != ext_plugin_status(EXT_PLUGIN_OK) { throw Self.failure(status) }
    }

    /// Makes this host a source of `registry`, in place of `serve` and before `start`: the guest's
    /// reverse calls are served by forward calls to the hosts registered there, along the declared
    /// edges, after `authorize` allowed them.
    public func attach(_ registry: WamrRegistry, authorize: @escaping (Data) -> Bool,
                       timeout: TimeInterval = 0, maxPayloadBytes: UInt32 = 0, maxInFlight: UInt32 = 0) throws {
        context.reverseAuthorize = authorize
        var services = services(timeout: timeout, maxPayloadBytes: maxPayloadBytes, maxInFlight: maxInFlight, handle: false)
        let status = ext_plugin_registry_attach(registry.handle, try live(), &services)
        if status != ext_plugin_status(EXT_PLUGIN_OK) { throw Self.failure(status) }
        sourceOf = registry
    }

    /// Calls an operation by contract and returns the complete response envelope; the engine
    /// supplies the request's identity, surface, deadline and id.
    public func call(contract name: String, version: String, operation: String, payload: Data? = nil,
                     timeout: TimeInterval = 30) throws -> Data {
        var input = #"{"contract":{"name":"\#(name)","version":"\#(version)"},"operation":"\#(operation)""#
        if let payload { input += #","payload":"# + String(decoding: payload, as: UTF8.self) }
        let request = Data((input + "}").utf8)
        var out = ext_plugin_buffer(data: nil, len: 0)
        defer { ext_plugin_buffer_free(&out) }
        let handle = try live()
        var options = ext_plugin_call_options(struct_size: UInt32(MemoryLayout<ext_plugin_call_options>.size),
                                       timeout_ms: Self.milliseconds(timeout), cancel: nil, value: nil)
        let status: ext_plugin_status = request.withUnsafeBytes { raw in
            ext_plugin_host_call(handle, raw.bindMemory(to: UInt8.self).baseAddress, raw.count, &options, &out)
        }
        guard status == ext_plugin_status(EXT_PLUGIN_OK), let data = out.data else { throw Self.failure(status) }
        return Data(bytes: data, count: out.len)
    }

    /// The engine host, for a registry that names it as a target.
    func registeredIn(_ registry: WamrRegistry, name: String, version: String) throws -> OpaquePointer {
        let handle = try live()
        lock.lock()
        registered.append((registry, name, version))
        lock.unlock()
        return handle
    }

    /// The registry removed a contract this host served.
    func forget(_ registry: WamrRegistry, name: String, version: String) {
        lock.lock()
        registered.removeAll { $0.registry === registry && $0.name == name && $0.version == version }
        lock.unlock()
    }

    private func services(timeout: TimeInterval, maxPayloadBytes: UInt32, maxInFlight: UInt32, handle: Bool) -> ext_plugin_host_services {
        ext_plugin_host_services(
            struct_size: UInt32(MemoryLayout<ext_plugin_host_services>.size),
            user: Unmanaged.passUnretained(context).toOpaque(),
            authorize: wamrReverseAuthorize, handle: handle ? wamrReverseHandle : nil,
            timeout_ms: timeout > 0 ? Self.milliseconds(timeout) : 0,
            max_payload_bytes: maxPayloadBytes, max_in_flight: maxInFlight, session_value: nil)
    }

    /// Loads the module, instantiates it and checks its handshake against the expected descriptor.
    public func start(timeout: TimeInterval = 30) throws {
        let milliseconds = Self.milliseconds(timeout)
        let status = ext_plugin_host_start(try live(), milliseconds)
        if status != ext_plugin_status(EXT_PLUGIN_OK) { throw Self.failure(status) }
    }

    /// Sends one complete ext.io.pc/v1 request and returns the complete response, including
    /// public errors. A call that fails after dispatch ends the session.
    public func invoke(_ request: Data, timeout: TimeInterval = 30) throws -> Data {
        var out = ext_plugin_buffer(data: nil, len: 0)
        defer { ext_plugin_buffer_free(&out) }
        let handle = try live()
        let status: ext_plugin_status = request.withUnsafeBytes { raw in
            ext_plugin_host_invoke(handle, raw.bindMemory(to: UInt8.self).baseAddress, raw.count, Self.milliseconds(timeout), &out)
        }
        guard status == ext_plugin_status(EXT_PLUGIN_OK), let data = out.data else { throw Self.failure(status) }
        return Data(bytes: data, count: out.len)
    }

    /// Stops admission and interrupts a running call. Idempotent and safe from any thread.
    public func close() {
        lock.lock()
        let handle = host
        closed = true
        lock.unlock()
        if let handle { ext_plugin_host_close(handle) }
    }

    /// The module's own descriptor JSON, by loading and handshaking it once. This is inspection:
    /// it asserts nothing about whether the module is trusted.
    public static func descriptor(of module: Data, timeout: TimeInterval = 30, options: Options = Options()) throws -> Data {
        let context = Context(verify: nil, authorize: nil, diagnostic: nil)
        var backend = ext_plugin_backend_extension_context()
        try makeBackend(module: module, options: options, context: context, into: &backend)
        defer { backend.release?(backend.user) }
        defer { backend.close?(backend.user) }
        let collector = Collector()
        var call = ext_plugin_call_options(struct_size: UInt32(MemoryLayout<ext_plugin_call_options>.size),
                                    timeout_ms: milliseconds(timeout), cancel: nil, value: nil)
        let status = backend.connect?(backend.user, &call, wamrEmit, Unmanaged.passUnretained(collector).toOpaque())
            ?? ext_plugin_status(EXT_PLUGIN_UNSUPPORTED)
        guard status == ext_plugin_status(EXT_PLUGIN_OK) else { throw failure(status) }
        return collector.data
    }

    // MARK: - Internals

    private func live() throws -> OpaquePointer {
        lock.lock()
        defer { lock.unlock() }
        guard !closed, let host else { throw Failure(status: Int32(EXT_PLUGIN_CLOSED), reason: "closed") }
        return host
    }

    private static func milliseconds(_ seconds: TimeInterval) -> UInt32 {
        UInt32(min(max(seconds * 1000, 1), Double(UInt32.max)))
    }

    private static func failure(_ status: ext_plugin_status) -> Failure {
        Failure(status: status, reason: ext_plugin_host_status_string(status).map { String(cString: $0) } ?? "unknown")
    }

    private static func makeBackend(module: Data, options: Options, context: Context, into backend: inout ext_plugin_backend_extension_context) throws {
        var native = ext_wamr_options()
        native.struct_size = UInt32(MemoryLayout<ext_wamr_options>.size)
        native.memory_limit_pages = options.memoryLimitPages
        native.stack_size = options.stackSize
        native.instruction_limit = options.instructionLimit
        native.max_diagnostic_bytes = options.maxDiagnosticBytes
        if context.diagnostic != nil {
            native.diagnostic = wamrDiagnostic
            native.diagnostic_user = Unmanaged.passUnretained(context).toOpaque()
        }
        let status: ext_plugin_status = module.withUnsafeBytes { raw in
            ext_wamr_backend_create(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, &native, &backend)
        }
        if status != ext_plugin_status(EXT_PLUGIN_OK) { throw failure(status) }
    }

    /// The closures the C callbacks reach. Kept alive by the host.
    final class Context {
        let verify: ((Data) -> Bool)?
        let authorize: ((Data) -> Bool)?
        let diagnostic: ((String) -> Void)?
        var reverseAuthorize: ((Data) -> Bool)?
        var reverseHandle: ((Data) throws -> ReverseReply)?
        init(verify: ((Data) -> Bool)?, authorize: ((Data) -> Bool)?, diagnostic: ((String) -> Void)?) {
            self.verify = verify
            self.authorize = authorize
            self.diagnostic = diagnostic
        }
    }

    final class Collector {
        var data = Data()
    }
}

private func wamrVerify(_ user: UnsafeMutableRawPointer?, _ json: UnsafePointer<UInt8>?, _ length: Int) -> Int32 {
    policy(user, json, length) { $0.verify }
}

private func wamrAuthorize(_ user: UnsafeMutableRawPointer?, _ json: UnsafePointer<UInt8>?, _ length: Int) -> Int32 {
    policy(user, json, length) { $0.authorize }
}

private func policy(_ user: UnsafeMutableRawPointer?, _ json: UnsafePointer<UInt8>?, _ length: Int,
                    _ pick: (WamrPluginHost.Context) -> ((Data) -> Bool)?) -> Int32 {
    guard let user, let check = pick(Unmanaged<WamrPluginHost.Context>.fromOpaque(user).takeUnretainedValue()) else { return 0 }
    let allowed = check(json.map { Data(bytes: $0, count: length) } ?? Data())
    return allowed ? 0 : Int32(EXT_PLUGIN_DENIED)
}

private func wamrDiagnostic(_ user: UnsafeMutableRawPointer?, _ data: UnsafePointer<UInt8>?, _ length: Int) {
    guard let user, let data else { return }
    let context = Unmanaged<WamrPluginHost.Context>.fromOpaque(user).takeUnretainedValue()
    context.diagnostic?(String(decoding: UnsafeBufferPointer(start: data, count: length), as: UTF8.self))
}

private func wamrEmit(_ context: UnsafeMutableRawPointer?, _ data: UnsafePointer<UInt8>?, _ length: Int) -> ext_plugin_status {
    guard let context, let data else { return ext_plugin_status(EXT_PLUGIN_INVALID) }
    Unmanaged<WamrPluginHost.Collector>.fromOpaque(context).takeUnretainedValue().data.append(data, count: length)
    return ext_plugin_status(EXT_PLUGIN_OK)
}

private func wamrReverseAuthorize(_ user: UnsafeMutableRawPointer?, _ options: UnsafePointer<ext_plugin_call_options>?,
                                  _ json: UnsafePointer<UInt8>?, _ length: Int) -> Int32 {
    guard let user, let check = Unmanaged<WamrPluginHost.Context>.fromOpaque(user).takeUnretainedValue().reverseAuthorize
    else { return 1 }
    return check(json.map { Data(bytes: $0, count: length) } ?? Data()) ? 0 : 1
}

private func wamrReverseHandle(_ user: UnsafeMutableRawPointer?, _ options: UnsafePointer<ext_plugin_call_options>?,
                               _ json: UnsafePointer<UInt8>?, _ length: Int,
                               _ emit: ext_plugin_reverse_emit?, _ sink: UnsafeMutableRawPointer?) -> ext_plugin_status {
    guard let user, let emit,
          let handle = Unmanaged<WamrPluginHost.Context>.fromOpaque(user).takeUnretainedValue().reverseHandle
    else { return ext_plugin_status(EXT_PLUGIN_IO) }
    guard let reply = try? handle(json.map { Data(bytes: $0, count: length) } ?? Data()) else {
        return ext_plugin_status(EXT_PLUGIN_IO) // the guest sees operation_failed
    }
    let (kind, body): (UInt32, Data)
    switch reply {
    case .payload(let data): (kind, body) = (UInt32(EXT_PLUGIN_HOST_REVERSE_PAYLOAD), data)
    case .publicError(let data): (kind, body) = (UInt32(EXT_PLUGIN_HOST_REVERSE_PUBLIC_ERROR), data)
    }
    return body.withUnsafeBytes { raw in emit(sink, kind, raw.bindMemory(to: UInt8.self).baseAddress, raw.count) }
}

/// A guest registry (ext_registry_* in ext_plugin_host.h) over WAMR hosts: a reverse call one guest makes
/// is served by a forward call to another guest, along an edge declared with `route`. An
/// undeclared edge is refused with reverse_undeclared, an unregistered target with
/// reverse_unavailable, and a call that would re-enter a host already on its call chain with
/// denied; the target's answer passes through unchanged. A host is a source once `attach` names
/// the registry, and a target once `register` names a contract it serves; it leaves the registry
/// when it is released. Hosts keep the registries they use alive.
public final class WamrRegistry: @unchecked Sendable {
    let handle: OpaquePointer
    private let lock = NSLock()
    private var targets: [String: WeakHost] = [:]
    private struct WeakHost { weak var host: WamrPluginHost? }

    public init() throws {
        var created: OpaquePointer?
        let status = ext_plugin_registry_create(&created)
        guard status == ext_plugin_status(EXT_PLUGIN_OK), let created else { throw WamrRegistry.failure(status) }
        handle = created
    }

    deinit { ext_plugin_registry_destroy(handle) }

    /// Names `host`, started and listing the contract, as the one serving `name`/`version`.
    public func register(name: String, version: String, host: WamrPluginHost) throws {
        let engineHost = try host.registeredIn(self, name: name, version: version)
        let status = ext_plugin_registry_register(handle, name, version, engineHost)
        if status != ext_plugin_status(EXT_PLUGIN_OK) {
            host.forget(self, name: name, version: version)
            throw WamrRegistry.failure(status)
        }
        lock.lock()
        targets[name + "\u{0}" + version] = WeakHost(host: host)
        lock.unlock()
    }

    /// Removes the contract and waits for the forward calls routed to its host.
    public func unregister(name: String, version: String) throws {
        let status = ext_plugin_registry_unregister(handle, name, version)
        if status != ext_plugin_status(EXT_PLUGIN_OK) { throw WamrRegistry.failure(status) }
        lock.lock()
        let host = targets.removeValue(forKey: name + "\u{0}" + version)?.host
        lock.unlock()
        host?.forget(self, name: name, version: version)
    }

    /// A reverse call of `fromOperation` in `from` is served by a call of `toOperation` in `to`.
    public func route(from: (name: String, version: String), fromOperation: String,
                      to: (name: String, version: String), toOperation: String) throws {
        let status: ext_plugin_status = from.name.withCString { fn in from.version.withCString { fv in fromOperation.withCString { fo in
            to.name.withCString { tn in to.version.withCString { tv in toOperation.withCString { to_ in
                var edge = ext_plugin_registry_edge(struct_size: UInt32(MemoryLayout<ext_plugin_registry_edge>.size),
                                             from_name: fn, from_version: fv, from_operation: fo,
                                             to_name: tn, to_version: tv, to_operation: to_)
                return ext_plugin_registry_route(handle, &edge)
            } } }
        } } }
        if status != ext_plugin_status(EXT_PLUGIN_OK) { throw WamrRegistry.failure(status) }
    }

    /// A target host is going away: leaves the contract, which waits for the calls routed to it.
    func leave(name: String, version: String, host: WamrPluginHost) {
        _ = ext_plugin_registry_unregister(handle, name, version)
        lock.lock()
        targets.removeValue(forKey: name + "\u{0}" + version)
        lock.unlock()
    }

    static func failure(_ status: ext_plugin_status) -> WamrPluginHost.Failure {
        WamrPluginHost.Failure(status: status, reason: ext_plugin_host_status_string(status).map { String(cString: $0) } ?? "unknown")
    }
}
