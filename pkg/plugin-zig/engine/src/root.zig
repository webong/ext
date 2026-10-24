//! Thin binding to the shared engine. FFI is generated from the C headers by
//! Zig's translate-c step. Handles and Buffer are owned and must not be copied;
//! call deinit exactly once after all concurrent uses have joined. No Go runtime.
const std = @import("std");
pub const ffi = @import("ffi");
pub const Error = error{ Invalid, Denied, Mismatch, Unsupported, Closed, Timeout, IO, OutOfMemory, Draining, NotFound, Ambiguous, Canceled, Updating, Capacity, Sequence, Protocol, Expired, UnknownStatus };
pub fn check(status: ffi.ext_status) Error!void {
    return switch (status) {
        ffi.EXT_OK => {},
        ffi.EXT_INVALID => error.Invalid,
        ffi.EXT_DENIED => error.Denied,
        ffi.EXT_MISMATCH => error.Mismatch,
        ffi.EXT_UNSUPPORTED => error.Unsupported,
        ffi.EXT_CLOSED => error.Closed,
        ffi.EXT_TIMEOUT => error.Timeout,
        ffi.EXT_IO => error.IO,
        ffi.EXT_NOMEM => error.OutOfMemory,
        ffi.EXT_DRAINING => error.Draining,
        ffi.EXT_NOT_FOUND => error.NotFound,
        ffi.EXT_AMBIGUOUS => error.Ambiguous,
        ffi.EXT_CANCELED => error.Canceled,
        ffi.EXT_UPDATING => error.Updating,
        ffi.EXT_CAPACITY => error.Capacity,
        ffi.EXT_SEQUENCE => error.Sequence,
        ffi.EXT_PROTOCOL => error.Protocol,
        ffi.EXT_EXPIRED => error.Expired,
        else => error.UnknownStatus,
    };
}
pub const Buffer = struct {
    raw: ffi.ext_buffer = .{ .data = null, .len = 0 },
    pub fn bytes(self: *const Buffer) []const u8 {
        return if (self.raw.len == 0) &.{} else self.raw.data[0..self.raw.len];
    }
    pub fn deinit(self: *Buffer) void {
        ffi.ext_buffer_free(&self.raw);
    }
};
pub const Cancellation = struct {
    raw: ?*ffi.ext_cancel,
    pub fn init() Error!Cancellation {
        var c: ?*ffi.ext_cancel = null;
        try check(ffi.ext_cancel_create(&c));
        return .{ .raw = c };
    }
    pub fn signal(self: *const Cancellation) void {
        ffi.ext_cancel_signal(self.raw);
    }
    pub fn isSignaled(self: *const Cancellation) bool {
        return ffi.ext_cancel_is_signaled(self.raw) != 0;
    }
    pub fn deinit(self: *Cancellation) void {
        ffi.ext_cancel_destroy(self.raw);
        self.raw = null;
    }
};
pub const CallOptions = struct {
    timeout_ms: u32 = 30_000,
    cancel: ?*const Cancellation = null,
    value: ?*anyopaque = null,
    fn raw(self: CallOptions) ffi.ext_call_options {
        return .{ .struct_size = @sizeOf(ffi.ext_call_options), .timeout_ms = self.timeout_ms, .cancel = if (self.cancel) |c| c.raw else null, .value = self.value };
    }
};
pub const Process = struct { executable: [:0]const u8, arguments: []const [*:0]const u8 = &.{}, environment: []const [*:0]const u8 = &.{} };
pub const Policies = struct { verify: ffi.ext_policy, authorize: ffi.ext_policy, user: ?*anyopaque = null };
/// Progress events of one call. A zero rate uses the default (50 events and 65536
/// bytes of text per second, with a burst of twice that); the rate travels to the
/// plugin in the request, so both ends apply the same one. `deliver` receives the
/// JSON of one event on the calling thread, or on an engine thread when the plugin
/// declares reverse operations, and never after the call returns. It must be quick
/// and must not reenter the host; `user` must live until the call returns. A null
/// `deliver` limits and discards the events.
pub const Progress = struct {
    events_per_second: u32 = 0,
    bytes_per_second: u32 = 0,
    deliver: ffi.ext_progress_fn = null,
    user: ?*anyopaque = null,
    fn raw(self: Progress) ffi.ext_progress_options {
        return .{ .struct_size = @sizeOf(ffi.ext_progress_options), .deliver = self.deliver, .user = self.user, .events_per_second = self.events_per_second, .bytes_per_second = self.bytes_per_second };
    }
};
/// The services a host offers its plugin: the operations its descriptor lists under
/// `reverse`. The engine decides admission (declared, within bounds, before the
/// plugin's deadline, fewer than `max_in_flight`) and refuses with a stable code;
/// `authorize` and `handle` are the host's policy. They run on engine threads, up
/// to `max_in_flight` at a time, and receive the request as JSON and an
/// `ext_call_options` whose `cancel` is an engine-owned signal to poll. `handle`
/// emits exactly one result through `emit` (a payload, or a public error) and
/// returns `EXT_OK`. Zero bounds use the defaults (30 s, 1 MiB, 4). `user` and
/// `session_value` must outlive the host.
pub const Services = struct {
    authorize: ffi.ext_context_policy,
    handle: ffi.ext_reverse_handler,
    user: ?*anyopaque = null,
    timeout_ms: u32 = 0,
    max_payload_bytes: u32 = 0,
    max_in_flight: u32 = 0,
    session_value: ?*anyopaque = null,
};
pub const Host = struct {
    raw: ?*ffi.ext_host,
    /// Callbacks and their user data must outlive Host. C snapshots strings and
    /// descriptor bytes. Policies must not reenter this same host.
    pub fn init(process: Process, descriptor: []const u8, policies: Policies) Error!Host {
        if (ffi.ext_host_abi_version() != ffi.EXT_HOST_ABI_VERSION) return error.Mismatch;
        const p: ffi.ext_jsonline_process_config = .{ .struct_size = @sizeOf(ffi.ext_jsonline_process_config), .executable = process.executable.ptr, .arguments = @ptrCast(process.arguments.ptr), .argument_count = process.arguments.len, .environment = @ptrCast(process.environment.ptr), .environment_count = process.environment.len };
        const b: ffi.ext_backend_options = .{ .struct_size = @sizeOf(ffi.ext_backend_options), .kind = ffi.EXT_BACKEND_JSONLINE_PROCESS_CONFIG, .config = &p, .config_size = @sizeOf(ffi.ext_jsonline_process_config) };
        const o: ffi.ext_host_options = .{ .abi_version = ffi.EXT_HOST_ABI_VERSION, .struct_size = @sizeOf(ffi.ext_host_options), .descriptor = descriptor.ptr, .descriptor_len = descriptor.len, .verify = policies.verify, .authorize = policies.authorize, .user = policies.user };
        var host: ?*ffi.ext_host = null;
        try check(ffi.ext_host_create(&o, &b, &host));
        return .{ .raw = host };
    }
    pub fn start(self: *const Host, options: CallOptions) Error!void {
        var o = options.raw();
        try check(ffi.ext_host_start_with_options(self.raw, &o));
    }
    /// Install contextual policy/observer callbacks before start. The caller
    /// owns callback state until deinit; observers must be fast and nonblocking.
    pub fn setHooks(self: *const Host, hooks: ffi.ext_host_hooks) Error!void {
        var h = hooks;
        h.struct_size = @sizeOf(ffi.ext_host_hooks);
        try check(ffi.ext_host_set_hooks(self.raw, &h));
    }
    pub fn invoke(self: *const Host, request: []const u8, options: CallOptions) Error!Buffer {
        var o = options.raw();
        var out: Buffer = .{};
        errdefer out.deinit();
        try check(ffi.ext_host_invoke_with_options(self.raw, request.ptr, request.len, &o, &out.raw));
        return out;
    }
    pub fn call(self: *const Host, request: []const u8, options: CallOptions) Error!Buffer {
        var o = options.raw();
        var out: Buffer = .{};
        errdefer out.deinit();
        try check(ffi.ext_host_call(self.raw, request.ptr, request.len, &o, &out.raw));
        return out;
    }
    /// Offer reverse-call services to the plugin. Install before start.
    pub fn setServices(self: *const Host, services: Services) Error!void {
        const s: ffi.ext_host_services = .{ .struct_size = @sizeOf(ffi.ext_host_services), .user = services.user, .authorize = services.authorize, .handle = services.handle, .timeout_ms = services.timeout_ms, .max_payload_bytes = services.max_payload_bytes, .max_in_flight = services.max_in_flight, .session_value = services.session_value };
        try check(ffi.ext_host_set_services(self.raw, &s));
    }
    /// `call` for an operation that declares progress: the engine validates the
    /// events, applies the rate limit and delivers what passes before this returns,
    /// ending with a marker for any it dropped.
    pub fn callProgress(self: *const Host, request: []const u8, options: CallOptions, progress: Progress) Error!Buffer {
        var o = options.raw();
        var p = progress.raw();
        var out: Buffer = .{};
        errdefer out.deinit();
        try check(ffi.ext_host_call_progress(self.raw, request.ptr, request.len, &o, &p, &out.raw));
        return out;
    }
    pub fn drain(self: *const Host, options: CallOptions) Error!void {
        var o = options.raw();
        try check(ffi.ext_host_drain_with_options(self.raw, &o));
    }
    pub fn close(self: *const Host) void {
        ffi.ext_host_close(self.raw);
    }
    pub fn state(self: *const Host) ffi.ext_host_state {
        return ffi.ext_host_get_state(self.raw);
    }
    pub fn deinit(self: *Host) void {
        ffi.ext_host_destroy(self.raw);
        self.raw = null;
    }
};
pub const Guest = struct {
    raw: ?*ffi.ext_guest,
    /// Handler receives borrowed request bytes and an engine copying result sink.
    /// It must not retain those pointers or unwind across the C ABI.
    pub fn init(options: ffi.ext_guest_options) Error!Guest {
        var guest: ?*ffi.ext_guest = null;
        try check(ffi.ext_guest_create(&options, &guest));
        return .{ .raw = guest };
    }
    pub fn descriptor(self: *const Guest) Error!Buffer {
        var out: Buffer = .{};
        errdefer out.deinit();
        try check(ffi.ext_guest_descriptor(self.raw, &out.raw));
        return out;
    }
    pub fn invoke(self: *const Guest, request: []const u8, timeout_ms: u32, context: ?*anyopaque) Error!Buffer {
        var out: Buffer = .{};
        errdefer out.deinit();
        try check(ffi.ext_guest_invoke(self.raw, request.ptr, request.len, timeout_ms, context, &out.raw));
        return out;
    }
    pub fn deinit(self: *Guest) void {
        ffi.ext_guest_destroy(self.raw);
        self.raw = null;
    }
};
pub fn service(operation: [:0]const u8, input: []const u8) Error!Buffer {
    var out: Buffer = .{};
    errdefer out.deinit();
    try check(ffi.ext_engine_call(operation.ptr, input.ptr, input.len, &out.raw));
    return out;
}
/// Loopback data endpoints: the engine's rules for admitting an endpoint a host
/// hands a plugin. An outcome is a status and never text, because an endpoint is a
/// credential: `error.Invalid` for a malformed one, `error.Denied` for one the
/// policy does not admit and `error.Expired` for one that has run out. Granting
/// the endpoint (its lifetime, one-time tokens, dialing) stays with the caller.
pub const endpoint = struct {
    /// Checks the shape rules alone: strict decoding, scheme, loopback address,
    /// path, token and expiry. `json` is the endpoint as JSON.
    pub fn check(json: []const u8) Error!void {
        var out = try service("endpoint.check", json);
        out.deinit();
    }
    /// Checks the shape rules and the policy, the JSON of
    /// `{"schemes":[...],"maxLifetimeMilliseconds":n,"now":"RFC 3339"}` with every
    /// field optional (a policy that names no scheme admits nothing). `scratch`
    /// holds the engine's input, `{"endpoint":...,"policy":...}`; `error.Capacity`
    /// when it is too small.
    pub fn validate(json: []const u8, policy: []const u8, scratch: []u8) Error!void {
        const head = "{\"endpoint\":";
        const middle = ",\"policy\":";
        const total = head.len + json.len + middle.len + policy.len + 1;
        if (total > scratch.len) return error.Capacity;
        var at: usize = 0;
        @memcpy(scratch[at .. at + head.len], head);
        at += head.len;
        @memcpy(scratch[at .. at + json.len], json);
        at += json.len;
        @memcpy(scratch[at .. at + middle.len], middle);
        at += middle.len;
        @memcpy(scratch[at .. at + policy.len], policy);
        at += policy.len;
        scratch[at] = '}';
        at += 1;
        var out = try service("endpoint.validate", scratch[0..at]);
        out.deinit();
    }
};
pub fn sha256(input: []const u8) [32]u8 {
    var result: [32]u8 = undefined;
    check(ffi.ext_engine_sha256(input.ptr, input.len, &result)) catch unreachable;
    return result;
}
pub fn verifyArtifacts(manifest: []const u8, root: [:0]const u8) Error!void {
    try check(ffi.ext_package_verify(manifest.ptr, manifest.len, root.ptr));
}
pub fn directoryDigest(root: [:0]const u8) Error![32]u8 {
    var result: [32]u8 = undefined;
    try check(ffi.ext_directory_digest(root.ptr, &result));
    return result;
}
test "shared services and cancellation" {
    var cancel = try Cancellation.init();
    defer cancel.deinit();
    try std.testing.expect(!cancel.isSignaled());
    cancel.signal();
    try std.testing.expect(cancel.isSignaled());
    var result = try service("protocol.negotiate", "{\"preferred\":[\"ext.plugin/v1\"],\"offered\":[\"ext.plugin/v1\"]}");
    defer result.deinit();
    try std.testing.expectEqualStrings("\"ext.plugin/v1\"", result.bytes());
    try std.testing.expectEqualSlices(u8, &.{ 0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41, 0x40, 0xde, 0x5d, 0xae, 0x22, 0x23, 0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17, 0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00, 0x15, 0xad }, &sha256("abc"));
}
fn denied(_: ?*anyopaque, _: [*c]const u8, _: usize) callconv(.c) i32 {
    return 1;
}
fn echo(_: ?*anyopaque, _: ?*anyopaque, _: [*c]const u8, _: usize, _: u32, emit: ffi.ext_guest_emit, sink: ?*anyopaque) callconv(.c) i32 {
    return emit.?(sink, ffi.EXT_GUEST_PAYLOAD, "7", 1);
}
const fixture = "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"test\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"test\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"}]}]}";
test "host and guest binding ownership" {
    var host = try Host.init(.{ .executable = "/not-launched" }, fixture, .{ .verify = denied, .authorize = denied });
    defer host.deinit();
    try std.testing.expectError(error.Denied, host.start(.{}));
    try std.testing.expectEqual(@as(ffi.ext_host_state, ffi.EXT_HOST_FAILED), host.state());
    try std.testing.expectError(error.Invalid, host.invoke("{}", .{}));
    try std.testing.expectError(error.Invalid, host.call("{}", .{}));
    try host.drain(.{});
    host.close();
    var guest = try Guest.init(.{ .abi_version = ffi.EXT_HOST_ABI_VERSION, .struct_size = @sizeOf(ffi.ext_guest_options), .descriptor = fixture, .descriptor_len = fixture.len, .max_call_ms = 1000, .user = null, .handle = echo });
    defer guest.deinit();
    var desc = try guest.descriptor();
    defer desc.deinit();
    try std.testing.expectEqualStrings(fixture, desc.bytes());
    var response = try guest.invoke("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"echo\",\"deadline\":\"2050-01-01T00:00:00Z\"}", 1000, null);
    defer response.deinit();
    try std.testing.expect(std.mem.indexOf(u8, response.bytes(), "\"payload\":7") != null);
}
fn counting(user: ?*anyopaque, _: ?*anyopaque, _: [*c]const u8, _: usize, _: u32, emit: ffi.ext_guest_emit, sink: ?*anyopaque) callconv(.c) i32 {
    const calls: *usize = @ptrCast(@alignCast(user.?));
    calls.* += 1;
    return emit.?(sink, ffi.EXT_GUEST_PAYLOAD, "7", 1);
}
fn conformanceRequest(buffer: []u8, id: []const u8, version: []const u8, operation: []const u8, deadline: []const u8) ![]const u8 {
    return std.fmt.bufPrint(buffer, "{{\"apiVersion\":\"{s}\",\"id\":\"{s}\",\"plugin\":{{\"id\":\"test\",\"revision\":\"r1\"}},\"contract\":{{\"name\":\"test\",\"version\":\"v1\"}},\"operation\":\"{s}\"{s}}}", .{ version, id, operation, deadline });
}
// A request that does not match the descriptor is answered by the engine, for every binding, as a
// public invalid_request response that echoes its id; one with no usable id, or that is not a
// request, is a failed call (error.Invalid). The handler is never called for either.
test "malformed requests" {
    var calls: usize = 0;
    var guest = try Guest.init(.{ .abi_version = ffi.EXT_HOST_ABI_VERSION, .struct_size = @sizeOf(ffi.ext_guest_options), .descriptor = fixture, .descriptor_len = fixture.len, .max_call_ms = 1000, .user = &calls, .handle = counting });
    defer guest.deinit();
    var first: [320]u8 = undefined;
    var second: [320]u8 = undefined;
    var third: [320]u8 = undefined;
    var fourth: [320]u8 = undefined;
    const deadline = ",\"deadline\":\"2099-01-01T00:00:00Z\"";
    var ok = try guest.invoke(try conformanceRequest(&first, "1", "ext.plugin/v1", "echo", deadline), 1000, null);
    defer ok.deinit();
    try std.testing.expect(std.mem.indexOf(u8, ok.bytes(), "\"payload\":7") != null);
    try std.testing.expectEqual(@as(usize, 1), calls);
    calls = 0;
    const mismatched = [_][]const u8{
        try conformanceRequest(&second, "1", "ext.plugin/v1", "nope", deadline),
        try conformanceRequest(&third, "1", "ext.plugin/v1", "echo", ""),
        try conformanceRequest(&fourth, "1", "ext.plugin/v2", "echo", deadline),
    };
    for (mismatched) |bad| {
        var reply = try guest.invoke(bad, 1000, null);
        defer reply.deinit();
        const text = reply.bytes();
        try std.testing.expect(std.mem.indexOf(u8, text, "\"id\":\"1\"") != null);
        try std.testing.expect(std.mem.indexOf(u8, text, "\"code\":\"invalid_request\"") != null);
        try std.testing.expect(std.mem.indexOf(u8, text, "\"payload\"") == null);
    }
    for ([_][]const u8{ "", "{", "[]", "7", "{}", "{\"id\":1}", "{\"id\":\"\"}", "{\"id\":\"-bad\"}" }) |bad| {
        try std.testing.expectError(error.Invalid, guest.invoke(bad, 1000, null));
    }
    try std.testing.expectEqual(@as(usize, 0), calls);
}

/// C owns instance admission, revisions and lifecycle. User callback state must
/// outlive the manager. Close, join calls/release leases, then deinit once.
pub const Instances = struct {
    raw: ?*ffi.ext_instances,
    pub fn init(options: ffi.ext_instance_options) Error!Instances {
        var o = options;
        o.struct_size = @sizeOf(ffi.ext_instance_options);
        var raw: ?*ffi.ext_instances = null;
        try check(ffi.ext_instances_create(&o, &raw));
        return .{ .raw = raw };
    }
    pub fn configure(self: *const Instances, key: []const u8, revision: []const u8, config: []const u8, options: CallOptions) Error!void {
        var o = options.raw();
        try check(ffi.ext_instances_configure(self.raw, key.ptr, key.len, revision.ptr, revision.len, config.ptr, config.len, &o));
    }
    pub fn acquire(self: *const Instances, key: []const u8) Error!Lease {
        var raw: ?*ffi.ext_lease = null;
        try check(ffi.ext_instances_acquire(self.raw, key.ptr, key.len, &raw));
        return .{ .raw = raw };
    }
    pub fn remove(self: *const Instances, key: []const u8) Error!void {
        try check(ffi.ext_instances_remove(self.raw, key.ptr, key.len));
    }
    pub fn close(self: *const Instances, options: CallOptions) Error!void {
        var o = options.raw();
        try check(ffi.ext_instances_close(self.raw, &o));
    }
    /// Draining leaves the handle owned by the caller; release leases and retry.
    pub fn deinit(self: *Instances) Error!void {
        try check(ffi.ext_instances_destroy(self.raw));
        self.raw = null;
    }
};
pub const Lease = struct {
    raw: ?*ffi.ext_lease,
    /// T must match the type produced by the factory. Borrowed until release.
    pub fn value(self: *const Lease, comptime T: type) ?*T {
        return @ptrCast(@alignCast(ffi.ext_lease_value(self.raw)));
    }
    pub fn revision(self: *const Lease) []const u8 {
        var n: usize = 0;
        const p = ffi.ext_lease_revision(self.raw, &n);
        return if (n == 0) &.{} else p[0..n];
    }
    /// Consumes the lease even if disposal returns an error. Do not copy leases.
    pub fn release(self: *Lease) Error!void {
        if (self.raw == null) return;
        const status = ffi.ext_lease_release(self.raw);
        self.raw = null;
        try check(status);
    }
};
pub const StreamID = struct {
    storage: [49]u8,
    pub fn text(self: *const StreamID) [:0]const u8 {
        return self.storage[0..48 :0];
    }
};
/// Close may run concurrently with reads; the reader callback must unblock.
/// Release runs after close and all reads join. Callback state outlives deinit.
pub const Streams = struct {
    raw: ?*ffi.ext_streams,
    pub fn init(options: ffi.ext_stream_options) Error!Streams {
        var o = options;
        o.struct_size = @sizeOf(ffi.ext_stream_options);
        var raw: ?*ffi.ext_streams = null;
        try check(ffi.ext_streams_create(&o, &raw));
        return .{ .raw = raw };
    }
    pub fn open(self: *const Streams, scope: []const u8, parameters: []const u8, options: CallOptions) Error!StreamID {
        var o = options.raw();
        var out: Buffer = .{};
        defer out.deinit();
        try check(ffi.ext_streams_open(self.raw, scope.ptr, scope.len, parameters.ptr, parameters.len, &o, &out.raw));
        const bytes = out.bytes();
        if (bytes.len != 50 or bytes[0] != '"' or bytes[49] != '"') return error.Mismatch;
        var id: StreamID = undefined;
        @memcpy(id.storage[0..48], bytes[1..49]);
        id.storage[48] = 0;
        return id;
    }
    pub fn read(self: *const Streams, scope: []const u8, id: [:0]const u8, sequence: u64, limit: u32, options: CallOptions) Error!Buffer {
        var o = options.raw();
        var out: Buffer = .{};
        errdefer out.deinit();
        try check(ffi.ext_streams_read(self.raw, scope.ptr, scope.len, id.ptr, sequence, limit, &o, &out.raw));
        return out;
    }
    pub fn remove(self: *const Streams, scope: []const u8, id: [:0]const u8) Error!void {
        try check(ffi.ext_streams_remove(self.raw, scope.ptr, scope.len, id.ptr));
    }
    pub fn close(self: *const Streams) Error!void {
        try check(ffi.ext_streams_close(self.raw));
    }
    pub fn deinit(self: *Streams) Error!void {
        try check(ffi.ext_streams_destroy(self.raw));
        self.raw = null;
    }
};
const ResourceFixture = struct {
    disposed: usize = 0,
    closed: usize = 0,
    value: u32 = 7,
    fn validate(_: ?*anyopaque, _: [*c]const u8, _: usize) callconv(.c) i32 {
        return ffi.EXT_OK;
    }
    fn create(user: ?*anyopaque, _: [*c]const ffi.ext_call_options, _: ?*const ffi.ext_cancel, _: [*c]const u8, _: usize, _: [*c]const u8, _: usize, out: [*c]?*anyopaque) callconv(.c) i32 {
        const self: *ResourceFixture = @ptrCast(@alignCast(user));
        out.* = &self.value;
        return ffi.EXT_OK;
    }
    fn dispose(user: ?*anyopaque, _: ?*anyopaque) callconv(.c) i32 {
        const self: *ResourceFixture = @ptrCast(@alignCast(user));
        self.disposed += 1;
        return ffi.EXT_OK;
    }
    fn open(user: ?*anyopaque, _: [*c]const ffi.ext_call_options, _: ?*const ffi.ext_cancel, _: [*c]const u8, _: usize, out: [*c]?*anyopaque) callconv(.c) i32 {
        out.* = user;
        return ffi.EXT_OK;
    }
    fn read(_: ?*anyopaque, _: ?*anyopaque, _: [*c]const ffi.ext_call_options, _: ?*const ffi.ext_cancel, _: u32, emit: ffi.ext_emit, sink: ?*anyopaque) callconv(.c) i32 {
        const data = "{\"items\":[7],\"done\":false}";
        return emit.?(sink, data, data.len);
    }
    fn close(user: ?*anyopaque, _: ?*anyopaque) callconv(.c) i32 {
        const self: *ResourceFixture = @ptrCast(@alignCast(user));
        self.closed += 1;
        return ffi.EXT_OK;
    }
    fn release(user: ?*anyopaque, _: ?*anyopaque) callconv(.c) void {
        const self: *ResourceFixture = @ptrCast(@alignCast(user));
        self.disposed += 1;
    }
};
test "instance leases and scoped stream ownership" {
    var fixture_state: ResourceFixture = .{};
    var instances = try Instances.init(.{ .struct_size = 0, .capacity = 2, .user = &fixture_state, .validate = ResourceFixture.validate, .create = ResourceFixture.create, .dispose = ResourceFixture.dispose, .observe = null });
    try instances.configure("key", "r1", "null", .{});
    var lease = try instances.acquire("key");
    try std.testing.expectEqual(@as(u32, 7), lease.value(u32).?.*);
    try std.testing.expectEqualStrings("r1", lease.revision());
    try instances.remove("key");
    try std.testing.expectEqual(@as(usize, 0), fixture_state.disposed);
    try lease.release();
    try lease.release();
    try instances.close(.{});
    try instances.deinit();
    try std.testing.expectEqual(@as(usize, 1), fixture_state.disposed);
    fixture_state.disposed = 0;
    var streams = try Streams.init(.{ .struct_size = 0, .capacity = 1, .max_age_ms = 10000, .user = &fixture_state, .open = ResourceFixture.open, .read = ResourceFixture.read, .close = ResourceFixture.close, .release = ResourceFixture.release });
    const id = try streams.open("alice", "null", .{});
    try std.testing.expectError(error.Denied, streams.read("bob", id.text(), 1, 1, .{}));
    var batch = try streams.read("alice", id.text(), 1, 1, .{});
    defer batch.deinit();
    try std.testing.expect(std.mem.indexOf(u8, batch.bytes(), "[7]") != null);
    try std.testing.expectError(error.Sequence, streams.read("alice", id.text(), 1, 1, .{}));
    try streams.remove("alice", id.text());
    try streams.close();
    try streams.deinit();
    try std.testing.expectEqual(@as(usize, 1), fixture_state.disposed);
    try std.testing.expectEqual(@as(usize, 1), fixture_state.closed);
}

// Progress events, reverse calls and data endpoints. The plugin is the C engine's
// scripted test guest (lib/plugin/tests/reverse.c), started as
// `<guest> --guest <mode>`. The tests need three paths from the environment and are
// skipped without them: EXT_REVERSE_TEST_GUEST (the built ext_reverse_test),
// EXT_REVERSE_TEST_DESCRIPTOR (the output of `ext_reverse_test --descriptor`) and,
// for the endpoint fixture, EXT_ENDPOINT_CASES (lib/plugin/testdata/v1/endpoint-cases.json).
// Files are read with libc, as the example host does.
const libc = struct {
    const FILE = opaque {};
    extern fn getenv([*c]const u8) ?[*:0]const u8;
    extern fn fopen([*c]const u8, [*c]const u8) ?*FILE;
    extern fn fclose(*FILE) c_int;
    extern fn fread([*c]u8, usize, usize, *FILE) usize;
};
fn environment(name: [*:0]const u8) ?[:0]const u8 {
    const value = libc.getenv(name) orelse return null;
    return std.mem.span(value);
}
fn readFile(path: [:0]const u8, buffer: []u8) ![]const u8 {
    const file = libc.fopen(path.ptr, "rb") orelse return error.ReadFailed;
    defer _ = libc.fclose(file);
    const n = libc.fread(buffer.ptr, 1, buffer.len, file);
    if (n == buffer.len) return error.ReadFailed; // too large for the buffer
    return buffer[0..n];
}
fn allow(_: ?*anyopaque, _: [*c]const u8, _: usize) callconv(.c) i32 {
    return 0;
}
const ReverseFixture = struct {
    authorized: usize = 0,
    handled: usize = 0,
    last: [512]u8 = undefined,
    last_len: usize = 0,
    fn authorize(user: ?*anyopaque, _: [*c]const ffi.ext_call_options, p: [*c]const u8, n: usize) callconv(.c) i32 {
        const self: *ReverseFixture = @ptrCast(@alignCast(user));
        self.authorized += 1;
        return if (std.mem.indexOf(u8, p[0..n], "\"operation\":\"secret\"") != null) 1 else 0;
    }
    fn handle(user: ?*anyopaque, _: [*c]const ffi.ext_call_options, p: [*c]const u8, n: usize, emit: ffi.ext_reverse_emit, sink: ?*anyopaque) callconv(.c) i32 {
        const self: *ReverseFixture = @ptrCast(@alignCast(user));
        self.handled += 1;
        const request = p[0..n];
        const keep = @min(request.len, self.last.len);
        @memcpy(self.last[0..keep], request[0..keep]);
        self.last_len = keep;
        if (std.mem.indexOf(u8, request, "\"operation\":\"sign\"") != null) return emit.?(sink, ffi.EXT_HOST_REVERSE_PAYLOAD, "\"signed\"", 8);
        if (std.mem.indexOf(u8, request, "\"operation\":\"errok\"") != null) {
            const failure = "{\"code\":\"policy\",\"message\":\"refused for you\",\"retryAfterMilliseconds\":5}";
            return emit.?(sink, ffi.EXT_HOST_REVERSE_PUBLIC_ERROR, failure, failure.len);
        }
        return ffi.EXT_IO;
    }
};
const Events = struct {
    count: usize = 0,
    text: [2][64]u8 = undefined,
    len: [2]usize = .{ 0, 0 },
    fn collect(user: ?*anyopaque, json: [*c]const u8, n: usize) callconv(.c) void {
        const self: *Events = @ptrCast(@alignCast(user));
        if (self.count < 2 and n <= 64) {
            @memcpy(self.text[self.count][0..n], json[0..n]);
            self.len[self.count] = n;
        }
        self.count += 1;
    }
};
const run_request = "{\"contract\":{\"name\":\"reverse.test\",\"version\":\"v1\"},\"operation\":\"run\",\"payload\":{}}";
fn openScripted(guest: [:0]const u8, descriptor: []const u8, mode: [*:0]const u8, services: ?*ReverseFixture) !Host {
    const arguments = [_][*:0]const u8{ "--guest", mode };
    var host = try Host.init(.{ .executable = guest, .arguments = &arguments }, descriptor, .{ .verify = allow, .authorize = allow });
    errdefer host.deinit();
    if (services) |fixture_state| {
        try host.setServices(.{ .authorize = ReverseFixture.authorize, .handle = ReverseFixture.handle, .user = fixture_state, .timeout_ms = 150, .max_payload_bytes = 64 });
    }
    try host.start(.{});
    return host;
}
test "reverse calls and progress events through a scripted plugin" {
    const guest = environment("EXT_REVERSE_TEST_GUEST") orelse return error.SkipZigTest;
    const descriptor_path = environment("EXT_REVERSE_TEST_DESCRIPTOR") orelse return error.SkipZigTest;
    var descriptor_buffer: [8192]u8 = undefined;
    const descriptor = try readFile(descriptor_path, &descriptor_buffer);
    // A request the host's hooks answer: the plugin's reply carries the answer.
    var state: ReverseFixture = .{};
    var host = try openScripted(guest, descriptor, "call:sign", &state);
    var reply = try host.call(run_request, .{});
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"id\":\"g1\",\"payload\":\"signed\"") != null);
    reply.deinit();
    try std.testing.expectEqual(@as(usize, 1), state.authorized);
    try std.testing.expectEqual(@as(usize, 1), state.handled);
    try std.testing.expect(std.mem.indexOf(u8, state.last[0..state.last_len], "\"surface\":\"signing\"") != null);
    try std.testing.expect(std.mem.indexOf(u8, state.last[0..state.last_len], "\"parent\":\"1\"") != null);
    host.deinit();
    // The policy denies before the handler runs.
    state = .{};
    host = try openScripted(guest, descriptor, "call:secret", &state);
    reply = try host.call(run_request, .{});
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"code\":\"denied\"") != null);
    reply.deinit();
    try std.testing.expectEqual(@as(usize, 0), state.handled);
    host.deinit();
    // A public error reaches the plugin unchanged; a failing handler does not.
    state = .{};
    host = try openScripted(guest, descriptor, "call:errok", &state);
    reply = try host.call(run_request, .{});
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"code\":\"policy\",\"message\":\"refused for you\",\"retryAfterMilliseconds\":5") != null);
    reply.deinit();
    host.deinit();
    host = try openScripted(guest, descriptor, "call:fail", &state);
    reply = try host.call(run_request, .{});
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"code\":\"operation_failed\"") != null);
    reply.deinit();
    host.deinit();
    // The engine refuses what the plugin did not declare, without asking the hooks.
    state = .{};
    host = try openScripted(guest, descriptor, "call:nothere", &state);
    reply = try host.call(run_request, .{});
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"code\":\"reverse_undeclared\"") != null);
    reply.deinit();
    try std.testing.expectEqual(@as(usize, 0), state.authorized);
    host.deinit();
    // A host that offers no services answers every request as unavailable.
    host = try openScripted(guest, descriptor, "call:sign", null);
    reply = try host.call(run_request, .{});
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"code\":\"reverse_unavailable\"") != null);
    reply.deinit();
    host.deinit();
    // Events arrive in order before the result, between reverse calls.
    state = .{};
    var events: Events = .{};
    host = try openScripted(guest, descriptor, "interleave", &state);
    reply = try host.callProgress(run_request, .{}, .{ .deliver = Events.collect, .user = &events });
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"payload\":\"signed\"") != null);
    reply.deinit();
    try std.testing.expectEqual(@as(usize, 2), events.count);
    try std.testing.expectEqualStrings("{\"kind\":\"log\",\"text\":\"one\"}", events.text[0][0..events.len[0]]);
    try std.testing.expectEqualStrings("{\"kind\":\"log\",\"text\":\"two\"}", events.text[1][0..events.len[1]]);
    // Without a callback the events are limited and discarded, and the call works.
    reply = try host.callProgress(run_request, .{}, .{});
    try std.testing.expect(std.mem.indexOf(u8, reply.bytes(), "\"payload\":\"signed\"") != null);
    reply.deinit();
    try std.testing.expectEqual(@as(usize, 2), events.count);
    host.deinit();
}
test "a plugin that breaks the framing ends the session" {
    const guest = environment("EXT_REVERSE_TEST_GUEST") orelse return error.SkipZigTest;
    const descriptor_path = environment("EXT_REVERSE_TEST_DESCRIPTOR") orelse return error.SkipZigTest;
    var descriptor_buffer: [8192]u8 = undefined;
    const descriptor = try readFile(descriptor_path, &descriptor_buffer);
    var state: ReverseFixture = .{};
    var host = try openScripted(guest, descriptor, "wrongparent", &state);
    defer host.deinit();
    try std.testing.expectError(error.Protocol, host.call(run_request, .{}));
    try std.testing.expectEqual(@as(ffi.ext_host_state, ffi.EXT_HOST_FAILED), host.state());
}
test "services are offered before the host starts" {
    var state: ReverseFixture = .{};
    var host = try Host.init(.{ .executable = "/not-launched" }, fixture, .{ .verify = allow, .authorize = allow });
    defer host.deinit();
    try host.setServices(.{ .authorize = ReverseFixture.authorize, .handle = ReverseFixture.handle, .user = &state });
    // A bound out of range is refused.
    try std.testing.expectError(error.Invalid, host.setServices(.{ .authorize = ReverseFixture.authorize, .handle = ReverseFixture.handle, .max_in_flight = 1025 }));
}
const sample_endpoint = "{\"scheme\":\"ws\",\"address\":\"localhost:80\",\"expiresAt\":\"2030-01-01T00:30:00Z\"}";
test "data endpoints answer with statuses" {
    var scratch: [1024]u8 = undefined;
    try endpoint.check(sample_endpoint);
    try std.testing.expectError(error.Invalid, endpoint.check("[]"));
    try std.testing.expectError(error.Invalid, endpoint.check(""));
    try endpoint.validate(sample_endpoint, "{\"schemes\":[\"ws\"],\"now\":\"2030-01-01T00:00:00Z\"}", &scratch);
    // A policy that names no scheme admits nothing.
    try std.testing.expectError(error.Denied, endpoint.validate(sample_endpoint, "{\"now\":\"2030-01-01T00:00:00Z\"}", &scratch));
    try std.testing.expectError(error.Expired, endpoint.validate(sample_endpoint, "{\"schemes\":[\"ws\"],\"now\":\"2030-01-01T01:00:00Z\"}", &scratch));
    try std.testing.expectError(error.Capacity, endpoint.validate(sample_endpoint, "{}", scratch[0..8]));
}
/// The end of the JSON value that starts at `i`.
fn valueEnd(b: []const u8, start: usize) usize {
    var i = start;
    var depth: usize = 0;
    var quoted = false;
    while (i < b.len) : (i += 1) {
        const c = b[i];
        if (quoted) {
            if (c == '\\') {
                i += 1;
            } else if (c == '"') {
                quoted = false;
                if (depth == 0) return i + 1;
            }
        } else switch (c) {
            '"' => quoted = true,
            '{', '[' => depth += 1,
            '}', ']' => {
                if (depth == 0) return i;
                depth -= 1;
                if (depth == 0) return i + 1;
            },
            ',' => {
                if (depth == 0) return i;
            },
            else => {},
        }
    }
    return i;
}
/// The raw JSON text of a top-level member of the object `s`.
fn member(s: []const u8, key: []const u8) []const u8 {
    var i: usize = std.mem.indexOfScalar(u8, s, '{').? + 1;
    while (i < s.len) {
        while (i < s.len and (s[i] == ' ' or s[i] == ',' or s[i] == '\t')) i += 1;
        if (i >= s.len or s[i] != '"') break;
        const name_end = std.mem.indexOfScalarPos(u8, s, i + 1, '"').?;
        const name = s[i + 1 .. name_end];
        i = name_end + 1;
        while (i < s.len and (s[i] == ' ' or s[i] == ':' or s[i] == '\t')) i += 1;
        const stop = valueEnd(s, i);
        if (std.mem.eql(u8, name, key)) return s[i..stop];
        i = stop;
    }
    return "";
}
fn outcome(result: Error!void) []const u8 {
    result catch |err| return switch (err) {
        error.Invalid => "invalid",
        error.Denied => "denied",
        error.Expired => "expired",
        else => "error",
    };
    return "ok";
}
test "data endpoints reproduce every outcome of the shared fixture" {
    const path = environment("EXT_ENDPOINT_CASES") orelse return error.SkipZigTest;
    var buffer: [262144]u8 = undefined;
    const data = try readFile(path, &buffer);
    var scratch: [4096]u8 = undefined;
    var total: usize = 0;
    var lines = std.mem.splitScalar(u8, data, '\n');
    while (lines.next()) |raw| {
        const line = std.mem.trim(u8, raw, " \t\r,");
        if (!std.mem.startsWith(u8, line, "{\"name\"")) continue;
        const endpoint_json = member(line, "endpoint");
        const policy = member(line, "policy");
        const want_check = std.mem.trim(u8, member(line, "check"), "\"");
        const want_validate = std.mem.trim(u8, member(line, "validate"), "\"");
        try std.testing.expectEqualStrings(want_check, outcome(endpoint.check(endpoint_json)));
        try std.testing.expectEqualStrings(want_validate, outcome(endpoint.validate(endpoint_json, policy, &scratch)));
        total += 1;
    }
    try std.testing.expectEqual(@as(usize, 213), total);
}
