//! Thin binding to the shared engine. FFI is generated from the C headers by
//! Zig's translate-c step. Handles and Buffer are owned and must not be copied;
//! call deinit exactly once after all concurrent uses have joined. No Go runtime.
const std = @import("std");
pub const ffi = @import("ffi");
pub const Error = error{ Invalid, Denied, Mismatch, Unsupported, Closed, Timeout, IO, OutOfMemory, Draining, NotFound, Ambiguous, Canceled, Updating, Capacity, Sequence, UnknownStatus };
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
