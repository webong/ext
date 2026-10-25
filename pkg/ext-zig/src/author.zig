const std = @import("std");
const w = @import("wire.zig");
const f = @import("frames.zig");
const A = w.Allocator;
// What a handler sees of its call. A call that came in on a connection that carries frames can also
// report progress and call its host (docs/plugin-guest-frames.md); this guest reads inline, so it
// makes one reverse call at a time and has no session-scoped caller.
pub const Context = struct {
    deadline: i128,
    remote: ?w.RemoteError = null,
    // Set by Guest.invokeWith when the request came in on a connection that carries frames.
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
        try w.validate(try self.descriptor(arena.allocator()));
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
        try w.validate(try self.descriptor(arena.allocator()));
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
    // Guest owns a deep snapshot. Registry storage may be freed immediately.
    pub fn guest(self: *const Registry, authorize: Policy) !Guest {
        const arena = try self.allocator.create(std.heap.ArenaAllocator);
        arena.* = std.heap.ArenaAllocator.init(self.allocator);
        errdefer {
            arena.deinit();
            self.allocator.destroy(arena);
        }
        const a = arena.allocator();
        const d = try w.typed(w.Descriptor, a, try w.decode(a, try w.encode(a, try self.descriptor(a))));
        try w.validate(d);
        const entries = try a.dupe(Entry, self.entries.items);
        for (entries) |*e| {
            e.contract.name = try a.dupe(u8, e.contract.name);
            e.contract.version = try a.dupe(u8, e.contract.version);
            e.operation.name = try a.dupe(u8, e.operation.name);
            e.operation.surface = try a.dupe(u8, e.operation.surface);
        }
        return .{ .arena = arena, .descriptor = d, .entries = entries, .authorize = authorize };
    }
};
pub const Guest = struct {
    arena: *std.heap.ArenaAllocator,
    descriptor: w.Descriptor,
    entries: []const Entry,
    authorize: Policy,
    pub fn deinit(self: *Guest) void {
        const a = self.arena.child_allocator;
        self.arena.deinit();
        a.destroy(self.arena);
        self.* = undefined;
    }
    pub fn invoke(self: *const Guest, a: A, r: w.Request) !w.Value {
        return self.invokeWith(a, r, null);
    }
    // As invoke, for a request that came in on a connection that carries frames, if there is one: the
    // call can then report progress and call its host.
    pub fn invokeWith(self: *const Guest, a: A, r: w.Request, link: ?*f.Link) !w.Value {
        w.validateRequest(self.descriptor, r) catch return w.response(a, r.id, null, .{ .code = "invalid_request", .message = "request does not match selected contract" });
        var ctx: Context = .{ .deadline = @min(try w.deadline(r.deadline), w.now() + w.timeout_ns) };
        // A marker for events never reported goes out before the response does.
        defer ctx.finish();
        if (link) |l| {
            ctx.allocator = a;
            ctx.link = l;
            ctx.descriptor = &self.descriptor;
            ctx.parent = r.id;
            // A call reports progress when its operation declared it and the host asked for events.
            if (r.progress) |rate| {
                if ((try w.lookup(self.descriptor, r.contract, r.operation)).progress) {
                    const limits = f.limitsOf(rate);
                    ctx.limits = limits;
                    ctx.limiter = f.Limiter.init(limits.events_per_second, limits.bytes_per_second, w.now());
                }
            }
        }
        const value = self.dispatch(a, &ctx, r) catch |err| {
            const remote = if (err == error.Public and ctx.remote != null) ctx.remote.? else w.RemoteError{ .code = "operation_failed", .message = "plugin operation failed" };
            return w.response(a, r.id, null, remote);
        };
        return w.response(a, r.id, value, null);
    }
    fn dispatch(self: *const Guest, a: A, ctx: *Context, r: w.Request) !w.Value {
        try ctx.check();
        try self.authorize(r);
        try ctx.check();
        for (self.entries) |e| if (w.eq(u8, e.contract.name, r.contract.name) and w.eq(u8, e.contract.version, r.contract.version) and w.eq(u8, e.operation.name, r.operation)) {
            const value = try e.handler(ctx, r, a);
            try ctx.check();
            return value;
        };
        return error.Unsupported;
    }
};
