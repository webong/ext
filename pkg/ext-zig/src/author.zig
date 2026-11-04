const std = @import("std");
const w = @import("wire.zig");
const f = @import("frames.zig");
const ffi = @import("ffi");
const engine = @import("engine.zig");
const A = w.Allocator;
// What a handler sees of its call. A call that came in on a connection that carries frames can also
// report progress and call its host (docs/plugin-guest-frames.md); this guest reads inline, so it
// makes one reverse call at a time and has no session-scoped caller.
pub const Context = struct {
    deadline: i128,
    remote: ?w.RemoteError = null,
    // Set by the guest's call handler when the request came in on a connection that carries frames.
    allocator: ?A = null,
    link: ?*f.Link = null,
    descriptor: ?*const w.Descriptor = null,
    parent: []const u8 = "",
    limits: ?f.Limits = null,
    limiter: ?f.Limiter = null,
    finished: bool = false,
    // The host's error to a reverse call, when it returned error.Remote.
    reverse_error: ?w.RemoteError = null,
    pub fn check(self: *const Context) !void {
        if (w.now() >= self.deadline) return error.Deadline;
    }
    pub fn fail(self: *Context, remote: w.RemoteError) error{Public} {
        self.remote = remote;
        return error.Public;
    }
    // The rate the host set for this call's events, or null when the call reports none.
    pub fn progressLimits(self: *const Context) ?f.Limits {
        return self.limits;
    }
    // Reports an event to the host while the call runs. Events reach the host in order, before the
    // result, within the rate limit the host set; .dropped is routine. Fails with error.Unsupported
    // when the operation did not declare progress, the host asked for no events or there is no frame
    // channel (stop reporting), error.Invalid for an event that breaks the rules (nothing is sent),
    // and error.Closed once the call is over or the connection ended.
    pub fn progress(self: *Context, event: f.Progress) !f.Outcome {
        const link = self.link orelse return error.Unsupported;
        const a = self.allocator orelse return error.Unsupported;
        const limiter = if (self.limiter) |*l| l else return error.Unsupported;
        const e: f.Event = .{ .kind = event.kind, .stream = event.stream, .text = event.text, .completed = event.completed, .total = event.total, .dropped = 0 };
        f.checkEvent(e) catch return error.Invalid;
        const dropped = limiter.admit(e.text.len, w.now()) orelse return .dropped;
        if (self.finished) return error.Closed;
        try f.sendEvent(link, a, self.parent, .{ .kind = e.kind, .stream = e.stream, .text = e.text, .completed = e.completed, .total = e.total, .dropped = dropped });
        return .sent;
    }
    // Calls an operation the host offers (declared in the descriptor's reverse list) on behalf of this
    // call, bound by its deadline, and waits for the answer. The host's refusal or error is left in
    // reverse_error with error.Remote (reverse_undeclared when the descriptor does not list it,
    // denied, busy, too_large, expired, ...). Fails with error.Unsupported when there is no frame
    // channel, error.Closed when the call is over or the connection ended, and error.Protocol when
    // the host broke the rules, which ends the session. The answer lives as long as allocator.
    pub fn reverse(self: *Context, ref: w.Ref, operation: []const u8, payload: w.Value) !w.Value {
        const link = self.link orelse return error.Unsupported;
        const a = self.allocator orelse return error.Unsupported;
        const d = self.descriptor orelse return error.Unsupported;
        if (self.finished) return error.Closed;
        _ = w.lookupReverse(d.*, ref, operation) catch {
            self.reverse_error = .{ .code = "reverse_undeclared", .message = "reverse call refused" };
            return error.Remote;
        };
        if (w.now() >= self.deadline) return error.Deadline;
        // Milliseconds, rounded down, so the request is never bound past the call's own deadline.
        const bound = @divFloor(self.deadline, 1_000_000) * 1_000_000;
        return link.reverse(a, self.parent, ref, operation, payload, bound, &self.reverse_error);
    }
    // Ends the call's progress: a marker for events never reported goes out, and nothing follows.
    fn finish(self: *Context) void {
        if (self.finished) return;
        self.finished = true;
        const link = self.link orelse return;
        const a = self.allocator orelse return;
        const limiter = if (self.limiter) |*l| l else return;
        const pending = limiter.pending();
        if (pending == 0) return;
        f.sendEvent(link, a, self.parent, .{ .kind = "log", .stream = "", .text = "", .completed = 0, .total = 0, .dropped = pending }) catch {};
    }
};
pub fn Method(comptime Input: type, comptime Output: type) type {
    return struct {
        contract: w.Ref,
        operation: w.Operation,
        validate_input: *const fn (Input) anyerror!void = struct {
            fn check(_: Input) !void {}
        }.check,
        validate_output: *const fn (Output) anyerror!void = struct {
            fn check(_: Output) !void {}
        }.check,
    };
}
const Entry = struct { contract: w.Ref, operation: w.Operation, handler: *const fn (*Context, w.Request, A) anyerror!w.Value };
pub const Policy = *const fn (w.Request) anyerror!void;
pub const Registry = struct {
    allocator: A,
    identity: w.Identity,
    entries: std.ArrayList(Entry) = .empty,
    reverse: std.ArrayList(w.Contract) = .empty,
    pub fn init(a: A, id: w.Identity) !Registry {
        try w.validateIdentity(id);
        return .{ .allocator = a, .identity = id };
    }
    pub fn deinit(self: *Registry) void {
        self.entries.deinit(self.allocator);
        self.reverse.deinit(self.allocator);
    }
    // Declares the host operations the plugin may call (Context.reverse). The slice must outlive guest().
    pub fn declareReverse(self: *Registry, ref: w.Ref, operations: []const w.Operation) !void {
        try self.reverse.append(self.allocator, .{ .name = ref.name, .version = ref.version, .operations = operations });
        errdefer _ = self.reverse.pop();
        var arena = std.heap.ArenaAllocator.init(self.allocator);
        defer arena.deinit();
        try w.validate(arena.allocator(), try self.descriptor(arena.allocator()));
    }
    pub fn register(self: *Registry, comptime Input: type, comptime Output: type, comptime method: Method(Input, Output), comptime handler: fn (*Context, w.Request, Input) anyerror!Output) !void {
        for (self.entries.items) |e| if (w.eq(u8, e.contract.name, method.contract.name) and w.eq(u8, e.contract.version, method.contract.version) and w.eq(u8, e.operation.name, method.operation.name)) return error.Invalid;
        const Dispatcher = struct {
            fn dispatch(ctx: *Context, r: w.Request, a: A) !w.Value {
                const input = w.typed(Input, a, r.payload) catch |err| switch (err) {
                    error.OutOfMemory => return err,
                    else => return ctx.fail(.{ .code = "invalid_input", .message = "request payload does not satisfy operation contract" }),
                };
                method.validate_input(input) catch return ctx.fail(.{ .code = "invalid_input", .message = "request payload does not satisfy operation contract" });
                const output = try handler(ctx, r, input);
                try method.validate_output(output);
                return w.decode(a, try w.encode(a, output));
            }
        };
        try self.entries.append(self.allocator, .{ .contract = method.contract, .operation = method.operation, .handler = Dispatcher.dispatch });
        errdefer _ = self.entries.pop();
        var arena = std.heap.ArenaAllocator.init(self.allocator);
        defer arena.deinit();
        try w.validate(arena.allocator(), try self.descriptor(arena.allocator()));
    }
    pub fn descriptor(self: *const Registry, a: A) !w.Descriptor {
        var contracts: std.ArrayList(w.Contract) = .empty;
        for (self.entries.items) |e| {
            var index: usize = 0;
            while (index < contracts.items.len) : (index += 1) {
                const c = contracts.items[index];
                if (w.eq(u8, c.name, e.contract.name) and w.eq(u8, c.version, e.contract.version)) break;
            }
            if (index == contracts.items.len) try contracts.append(a, .{ .name = e.contract.name, .version = e.contract.version, .operations = &.{} });
            const c = &contracts.items[index];
            const ops = try a.alloc(w.Operation, c.operations.len + 1);
            @memcpy(ops[0..c.operations.len], c.operations);
            ops[c.operations.len] = e.operation;
            c.operations = ops;
        }
        return .{ .identity = self.identity, .contracts = contracts.items, .reverse = self.reverse.items };
    }
    // Guest owns a deep snapshot. Registry storage may be freed immediately. The engine validates the
    // descriptor once, here, and every request against it afterwards.
    pub fn guest(self: *const Registry, authorize: Policy) !Guest {
        const arena = try self.allocator.create(std.heap.ArenaAllocator);
        arena.* = std.heap.ArenaAllocator.init(self.allocator);
        errdefer {
            arena.deinit();
            self.allocator.destroy(arena);
        }
        const a = arena.allocator();
        const d = try w.typed(w.Descriptor, a, try w.decode(a, try w.encode(a, try self.descriptor(a))));
        const json = try w.encode(a, d);
        const entries = try a.dupe(Entry, self.entries.items);
        for (entries) |*e| {
            e.contract.name = try a.dupe(u8, e.contract.name);
            e.contract.version = try a.dupe(u8, e.contract.version);
            e.operation.name = try a.dupe(u8, e.operation.name);
            e.operation.surface = try a.dupe(u8, e.operation.surface);
        }
        const state = try a.create(State);
        state.* = .{ .descriptor = d, .entries = entries, .authorize = authorize };
        var handle: ?*ffi.ext_guest = null;
        const options: ffi.ext_guest_options = .{
            .abi_version = ffi.EXT_HOST_ABI_VERSION,
            .struct_size = @sizeOf(ffi.ext_guest_options),
            .descriptor = json.ptr,
            .descriptor_len = json.len,
            .max_call_ms = call_limit_ms,
            .user = state,
            .handle = handle_call,
        };
        try engine.check(ffi.ext_guest_create(&options, &handle));
        return .{ .arena = arena, .descriptor = d, .handle = handle.? };
    }
};
// The longest a call may run, as the engine bounds it; a request's own deadline can shorten it.
const call_limit_ms = 30_000;

// What the engine hands the handler as call_user: the one call being run.
const Call = struct { allocator: A, link: ?*f.Link };

// What the handler reaches from the engine's user pointer. It lives in the guest's arena, so the pointer
// stays valid wherever the Guest value is moved to.
const State = struct {
    descriptor: w.Descriptor,
    entries: []const Entry,
    authorize: Policy,
    fn dispatch(self: *const State, a: A, ctx: *Context, r: w.Request) !w.Value {
        try ctx.check();
        try self.authorize(r);
        try ctx.check();
        for (self.entries) |e| if (w.eq(u8, e.contract.name, r.contract.name) and w.eq(u8, e.contract.version, r.contract.version) and w.eq(u8, e.operation.name, r.operation)) {
            // A handler that returns after its time is answered by the engine, not here.
            return e.handler(ctx, r, a);
        };
        return error.Unsupported;
    }
};

// The engine calls this for a request it has already matched to the descriptor, with the time the call
// has left. It emits the payload or a public error; any other failure is a status the engine turns
// into the sanitized operation_failed answer.
fn handle_call(user: ?*anyopaque, call_user: ?*anyopaque, request: [*c]const u8, len: usize, remaining_ms: u32, emit: ffi.ext_guest_emit, sink: ?*anyopaque) callconv(.c) ffi.ext_status {
    const state: *const State = @ptrCast(@alignCast(user.?));
    const call: *const Call = @ptrCast(@alignCast(call_user.?));
    return run(state, call, request[0..len], remaining_ms, emit.?, sink) catch ffi.EXT_IO;
}
fn run(state: *const State, call: *const Call, bytes: []const u8, remaining_ms: u32, emit: *const fn (?*anyopaque, u32, [*c]const u8, usize) callconv(.c) ffi.ext_status, sink: ?*anyopaque) !ffi.ext_status {
    const a = call.allocator;
    const r = try w.requestFromValue(try w.decode(a, bytes));
    // The engine counts whole milliseconds and rounds up, so the request's own deadline, to the
    // nanosecond, still bounds the call (a reverse call is never bound past it); the engine's time left
    // can only shorten it.
    var ctx: Context = .{ .deadline = @min(try w.deadline(r.deadline), w.now() + @as(i128, remaining_ms) * 1_000_000) };
    // A marker for events never reported goes out before the response does.
    defer ctx.finish();
    if (call.link) |l| {
        ctx.allocator = a;
        ctx.link = l;
        ctx.descriptor = &state.descriptor;
        ctx.parent = r.id;
        // A call reports progress when its operation declared it and the host asked for events.
        if (r.progress) |rate| {
            if ((try w.lookup(state.descriptor, r.contract, r.operation)).progress) {
                const limits = f.limitsOf(rate);
                ctx.limits = limits;
                ctx.limiter = f.Limiter.init(limits.events_per_second, limits.bytes_per_second, w.now());
            }
        }
    }
    const value = state.dispatch(a, &ctx, r) catch |err| {
        if (err == error.Public) if (ctx.remote) |remote| {
            const body = try w.encode(a, remote);
            return emit(sink, ffi.EXT_GUEST_PUBLIC_ERROR, body.ptr, body.len);
        };
        return ffi.EXT_IO;
    };
    const body = try w.encode(a, value);
    return emit(sink, ffi.EXT_GUEST_PAYLOAD, body.ptr, body.len);
}

pub const Guest = struct {
    arena: *std.heap.ArenaAllocator,
    descriptor: w.Descriptor,
    handle: *ffi.ext_guest,
    pub fn deinit(self: *Guest) void {
        ffi.ext_guest_destroy(self.handle);
        const a = self.arena.child_allocator;
        self.arena.deinit();
        a.destroy(self.arena);
        self.* = undefined;
    }
    // Runs one request, given as the JSON the host sent, and returns the response as JSON. The engine
    // answers a request that does not match the descriptor with invalid_request, and one that failed
    // or ran out of time with operation_failed. A request with no usable id has nothing to echo, so it
    // is error.Invalid. link is the connection the request came in on, if it carries frames: the call can
    // then report progress and call its host.
    pub fn invokeBytes(self: *const Guest, a: A, request: []const u8, link: ?*f.Link) ![]u8 {
        var call: Call = .{ .allocator = a, .link = link };
        var out: ffi.ext_buffer = .{ .data = null, .len = 0 };
        defer ffi.ext_buffer_free(&out);
        try engine.check(ffi.ext_guest_invoke(self.handle, request.ptr, request.len, call_limit_ms, &call, &out));
        return a.dupe(u8, if (out.len == 0) &.{} else out.data[0..out.len]);
    }
    // As invokeBytes, for a request already built and a response wanted as a tree.
    pub fn invoke(self: *const Guest, a: A, r: w.Request) !w.Value {
        return w.decode(a, try self.invokeBytes(a, try w.encode(a, r), null));
    }
};
