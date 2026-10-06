const std = @import("std");
const w = @import("wire.zig");
const author = @import("author.zig");
const jsonline = @import("jsonline.zig");
pub const Backend = struct {
    context: *anyopaque,
    exchange: *const fn (*anyopaque, w.Allocator, u32, []const u8, i128) anyerror![]u8,
    close: *const fn (*anyopaque) void,
};
pub const Connector = struct { context: *anyopaque, connect: *const fn (*anyopaque) anyerror!Backend };
pub fn Result(comptime T: type) type {
    return union(enum) { value: T, remote: w.RemoteError };
}
pub const Session = struct {
    arena: *std.heap.ArenaAllocator,
    descriptor: w.Descriptor,
    backend: Backend,
    authorize: author.Policy,
    next: u64 = 0,
    closed: bool = false,
    pub fn open(a: w.Allocator, selected: w.Descriptor, verify: *const fn (w.Descriptor) anyerror!void, connector: Connector, authorize: author.Policy) !Session {
        const arena = try a.create(std.heap.ArenaAllocator);
        arena.* = std.heap.ArenaAllocator.init(a);
        errdefer {
            arena.deinit();
            a.destroy(arena);
        }
        const own = arena.allocator();
        const d = try w.typed(w.Descriptor, own, try w.decode(own, try w.encode(own, selected)));
        try w.validate(d);
        try verify(d);
        const backend = try connector.connect(connector.context);
        errdefer backend.close(backend.context);
        var scratch = std.heap.ArenaAllocator.init(a);
        defer scratch.deinit();
        const temp = scratch.allocator();
        const deadline = w.now() + w.timeout_ns;
        const bytes = try backend.exchange(backend.context, temp, 1, try w.encode(temp, .{ .deadline = try w.timestamp(temp, deadline) }), deadline);
        try w.match(d, try w.typed(w.Descriptor, temp, try w.decode(temp, bytes)));
        if (w.now() >= deadline) return error.Deadline;
        return .{ .arena = arena, .descriptor = d, .backend = backend, .authorize = authorize };
    }
    pub fn close(self: *Session) void {
        if (!self.closed) {
            self.closed = true;
            self.backend.close(self.backend.context);
        }
    }
    pub fn deinit(self: *Session) void {
        self.close();
        const a = self.arena.child_allocator;
        self.arena.deinit();
        a.destroy(self.arena);
        self.* = undefined;
    }
    // Serial API. Returned payload/remote strings live in the caller's arena.
    pub fn callRaw(self: *Session, a: w.Allocator, ref: w.Ref, operation: []const u8, payload: w.Value, timeout_ns: i128) !Result(w.Value) {
        if (self.closed) return error.Closed;
        if (timeout_ns <= 0) return error.Deadline;
        const deadline = w.now() + @min(timeout_ns, w.timeout_ns);
        if (self.next == std.math.maxInt(u64)) return error.Invalid;
        self.next += 1;
        const r: w.Request = .{ .apiVersion = w.version, .id = try std.fmt.allocPrint(a, "{d}", .{self.next}), .plugin = self.descriptor.identity, .contract = ref, .operation = operation, .surface = (try w.lookup(self.descriptor, ref, operation)).surface, .deadline = try w.timestamp(a, deadline), .payload = payload };
        try w.validateRequest(self.descriptor, r);
        try self.authorize(r);
        if (w.now() >= deadline) return error.Deadline;
        const input = try w.encode(a, r);
        const value = self.dispatch(a, input, r.id, deadline) catch |err| {
            self.close();
            return err;
        };
        if (value.object.get("error")) |e| if (e != .null) return .{ .remote = try w.typed(w.RemoteError, a, e) };
        return .{ .value = try w.field(value, "payload") };
    }
    fn dispatch(self: *Session, a: w.Allocator, input: []const u8, id: []const u8, deadline: i128) !w.Value {
        const bytes = try self.backend.exchange(self.backend.context, a, 2, input, deadline);
        if (w.now() >= deadline) return error.Deadline;
        const value = try w.decode(a, bytes);
        try w.validateResponse(a, value, id);
        return value;
    }
    pub fn call(self: *Session, comptime Input: type, comptime Output: type, a: w.Allocator, method: author.Method(Input, Output), input: Input) !Result(Output) {
        try method.validate_input(input);
        const result = try self.callRaw(a, method.contract, method.operation.name, try w.decode(a, try w.encode(a, input)), w.timeout_ns);
        return switch (result) {
            .remote => |r| .{ .remote = r },
            .value => |v| blk: {
                const out = try w.typed(Output, a, v);
                try method.validate_output(out);
                break :blk .{ .value = out };
            },
        };
    }
};
pub const JSONLine = struct {
    connection: *jsonline.Connection,
    pub fn backend(self: *JSONLine) Backend {
        return .{ .context = self, .exchange = exchange, .close = close };
    }
    fn close(ctx: *anyopaque) void {
        const self: *JSONLine = @ptrCast(@alignCast(ctx));
        self.connection.close();
    }
    fn exchange(ctx: *anyopaque, a: w.Allocator, op: u32, input: []const u8, deadline: i128) ![]u8 {
        const self: *JSONLine = @ptrCast(@alignCast(ctx));
        const req = if (op == 1) blk: {
            const hello = try w.decode(a, input);
            break :blk try w.encode(a, .{ .apiVersion = w.version, .id = "hello", .operation = "plugin.hello", .deadline = try w.string(try w.field(hello, "deadline")) });
        } else input;
        try self.connection.writeFrame(req, deadline);
        const bytes = (try self.connection.readFrame(a, deadline)) orelse return error.Transport;
        if (op == 1) {
            const v = try w.decode(a, bytes);
            try w.validateResponse(a, v, "hello");
            if (v.object.get("error")) |e| if (e != .null) return error.Transport;
            return w.encode(a, try w.field(v, "payload"));
        }
        return bytes;
    }
};
// Trusted native code only. Loading and C execution are synchronous and must
// honor deadlines; they cannot be forcibly interrupted. Images stay resident.
pub const CShared = struct {
    handle: u64,
    invoke: *const fn (u64, u32, [*]const u8, u32, [*]u8, u32, *u32) callconv(.c) u32,
    release: *const fn (u64) callconv(.c) void,
    closed: bool = false,
    pub fn open(path: []const u8) !CShared {
        if (!std.fs.path.isAbsolute(path) or std.mem.indexOfScalar(u8, path, 0) != null) return error.Invalid;
        var library = try std.DynLib.open(path); // Never close, even on ABI failure.
        const version = library.lookup(*const fn () callconv(.c) u32, "ctx_plugin_abi_version") orelse return error.Unsupported;
        const create = library.lookup(*const fn () callconv(.c) u64, "ctx_plugin_open") orelse return error.Unsupported;
        const invoke = library.lookup(*const fn (u64, u32, [*]const u8, u32, [*]u8, u32, *u32) callconv(.c) u32, "ctx_plugin_call") orelse return error.Unsupported;
        const release = library.lookup(*const fn (u64) callconv(.c) void, "ctx_plugin_close") orelse return error.Unsupported;
        if (version() != 1) return error.Unsupported;
        const handle = create();
        if (handle == 0) return error.Denied;
        return .{ .handle = handle, .invoke = invoke, .release = release };
    }
    pub fn backend(self: *CShared) Backend {
        return .{ .context = self, .exchange = exchange, .close = close };
    }
    fn close(ctx: *anyopaque) void {
        const self: *CShared = @ptrCast(@alignCast(ctx));
        if (!self.closed) {
            self.closed = true;
            self.release(self.handle);
        }
    }
    fn exchange(ctx: *anyopaque, a: w.Allocator, op: u32, input: []const u8, deadline: i128) ![]u8 {
        const self: *CShared = @ptrCast(@alignCast(ctx));
        if (self.closed) return error.Closed;
        if (input.len > w.max_frame) return error.Invalid;
        if (w.now() >= deadline) return error.Deadline;
        const out = try a.alloc(u8, w.max_frame);
        var len: u32 = 0;
        const status = self.invoke(self.handle, op, input.ptr, @intCast(input.len), out.ptr, w.max_frame, &len);
        if (w.now() >= deadline) return error.Deadline;
        if (status != 0 or len == 0 or len > w.max_frame) return error.Transport;
        return out[0..len];
    }
};
