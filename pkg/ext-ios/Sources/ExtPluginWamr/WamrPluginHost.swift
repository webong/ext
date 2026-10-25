import CExtWamr
import Foundation

/// Hosts an ext.plugin/v1 WebAssembly reactor (docs/plugin-reactor-abi.md) in this process, on
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
    /// Why the engine refused an operation: its ext_status value and name.
    public struct Failure: Error, Equatable, CustomStringConvertible {
        public let status: Int32
        public let reason: String
        public var description: String { "ext_status \(status) (\(reason))" }
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

    private let context: Context
    private var host: OpaquePointer?
    private let lock = NSLock()
    private var closed = false

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
        var backend = ext_backend_extension_context()
        try Self.makeBackend(module: module, options: options, context: context, into: &backend)
        let user = Unmanaged.passUnretained(context).toOpaque()
        var created: OpaquePointer?
        let status: ext_status = descriptor.withUnsafeBytes { raw in
            var hostOptions = ext_host_options(
                abi_version: UInt32(EXT_HOST_ABI_VERSION),
                struct_size: UInt32(MemoryLayout<ext_host_options>.size),
                descriptor: raw.bindMemory(to: UInt8.self).baseAddress, descriptor_len: raw.count,
                verify: wamrVerify, authorize: wamrAuthorize, user: user)
            return withUnsafePointer(to: &backend) { pointer in
                var selected = ext_backend_options(
                    struct_size: UInt32(MemoryLayout<ext_backend_options>.size),
                    kind: UInt32(EXT_BACKEND_EXTENSION_CONTEXT), config: UnsafeRawPointer(pointer),
                    config_size: MemoryLayout<ext_backend_extension_context>.size)
                return ext_host_create(&hostOptions, &selected, &created)
            }
        }
        guard status == ext_status(EXT_OK), let created else {
            // The engine did not take ownership of the backend, so release it here.
            backend.release?(backend.user)
            throw Self.failure(status)
        }
        host = created
    }

    deinit {
        close()
        lock.lock()
        defer { lock.unlock() }
        if let host { ext_host_destroy(host) }
        host = nil
    }

    /// Loads the module, instantiates it and checks its handshake against the expected descriptor.
    public func start(timeout: TimeInterval = 30) throws {
        let milliseconds = Self.milliseconds(timeout)
        let status = ext_host_start(try live(), milliseconds)
        if status != ext_status(EXT_OK) { throw Self.failure(status) }
    }

    /// Sends one complete ext.plugin/v1 request and returns the complete response, including
    /// public errors. A call that fails after dispatch ends the session.
    public func invoke(_ request: Data, timeout: TimeInterval = 30) throws -> Data {
        var out = ext_buffer(data: nil, len: 0)
        defer { ext_buffer_free(&out) }
        let handle = try live()
        let status: ext_status = request.withUnsafeBytes { raw in
            ext_host_invoke(handle, raw.bindMemory(to: UInt8.self).baseAddress, raw.count, Self.milliseconds(timeout), &out)
        }
        guard status == ext_status(EXT_OK), let data = out.data else { throw Self.failure(status) }
        return Data(bytes: data, count: out.len)
    }

    /// Stops admission and interrupts a running call. Idempotent and safe from any thread.
    public func close() {
        lock.lock()
        let handle = host
        closed = true
        lock.unlock()
        if let handle { ext_host_close(handle) }
    }

    /// The module's own descriptor JSON, by loading and handshaking it once. This is inspection:
    /// it asserts nothing about whether the module is trusted.
    public static func descriptor(of module: Data, timeout: TimeInterval = 30, options: Options = Options()) throws -> Data {
        let context = Context(verify: nil, authorize: nil, diagnostic: nil)
        var backend = ext_backend_extension_context()
        try makeBackend(module: module, options: options, context: context, into: &backend)
        defer { backend.release?(backend.user) }
        defer { backend.close?(backend.user) }
        let collector = Collector()
        var call = ext_call_options(struct_size: UInt32(MemoryLayout<ext_call_options>.size),
                                    timeout_ms: milliseconds(timeout), cancel: nil, value: nil)
        let status = backend.connect?(backend.user, &call, wamrEmit, Unmanaged.passUnretained(collector).toOpaque())
            ?? ext_status(EXT_UNSUPPORTED)
        guard status == ext_status(EXT_OK) else { throw failure(status) }
        return collector.data
    }

    // MARK: - Internals

    private func live() throws -> OpaquePointer {
        lock.lock()
        defer { lock.unlock() }
        guard !closed, let host else { throw Failure(status: Int32(EXT_CLOSED), reason: "closed") }
        return host
    }

    private static func milliseconds(_ seconds: TimeInterval) -> UInt32 {
        UInt32(min(max(seconds * 1000, 1), Double(UInt32.max)))
    }

    private static func failure(_ status: ext_status) -> Failure {
        Failure(status: status, reason: ext_host_status_string(status).map { String(cString: $0) } ?? "unknown")
    }

    private static func makeBackend(module: Data, options: Options, context: Context, into backend: inout ext_backend_extension_context) throws {
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
        let status: ext_status = module.withUnsafeBytes { raw in
            ext_wamr_backend_create(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, &native, &backend)
        }
        if status != ext_status(EXT_OK) { throw failure(status) }
    }

    /// The closures the C callbacks reach. Kept alive by the host.
    final class Context {
        let verify: ((Data) -> Bool)?
        let authorize: ((Data) -> Bool)?
        let diagnostic: ((String) -> Void)?
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
    return allowed ? 0 : Int32(EXT_DENIED)
}

private func wamrDiagnostic(_ user: UnsafeMutableRawPointer?, _ data: UnsafePointer<UInt8>?, _ length: Int) {
    guard let user, let data else { return }
    let context = Unmanaged<WamrPluginHost.Context>.fromOpaque(user).takeUnretainedValue()
    context.diagnostic?(String(decoding: UnsafeBufferPointer(start: data, count: length), as: UTF8.self))
}

private func wamrEmit(_ context: UnsafeMutableRawPointer?, _ data: UnsafePointer<UInt8>?, _ length: Int) -> ext_status {
    guard let context, let data else { return ext_status(EXT_INVALID) }
    Unmanaged<WamrPluginHost.Collector>.fromOpaque(context).takeUnretainedValue().data.append(data, count: length)
    return ext_status(EXT_OK)
}
