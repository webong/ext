const std = @import("std");
const w = @import("wire.zig");
const A = w.Allocator;
pub const Context = struct {
    deadline: i128,
    remote: ?w.RemoteError = null,
    pub fn check(self: *const Context) !void {
        if (w.now() >= self.deadline) return error.Deadline;
    }
    pub fn fail(self: *Context, remote: w.RemoteError) error{Public} {
        self.remote = remote;
        return error.Public;
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
    pub fn init(a: A, id: w.Identity) !Registry {
        try w.validateIdentity(id);
        return .{ .allocator = a, .identity = id };
    }
    pub fn deinit(self: *Registry) void {
        self.entries.deinit(self.allocator);
    }
    pub fn register(self: *Registry, comptime Input: type, comptime Output: type, comptime method: Method(Input, Output), comptime handler: fn (*Context, w.Request, Input) anyerror!Output) !void {
        for (self.entries.items) |e| if (w.eq(u8, e.contract.name, method.contract.name) and w.eq(u8, e.contract.version, method.contract.version) and w.eq(u8, e.operation.name, method.operation.name)) return error.Invalid;
        const Dispatcher = struct {
            fn dispatch(ctx: *Context, r: w.Request, a: A) !w.Value {
                const input = try w.typed(Input, a, r.payload);
                try method.validate_input(input);
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
        return .{ .identity = self.identity, .contracts = contracts.items };
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
        w.validateRequest(self.descriptor, r) catch return w.response(a, r.id, null, .{ .code = "invalid_request", .message = "request does not match selected contract" });
        var ctx: Context = .{ .deadline = @min(try w.deadline(r.deadline), w.now() + w.timeout_ns) };
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
