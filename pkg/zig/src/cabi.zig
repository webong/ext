const std = @import("std");
const w = @import("wire.zig");
const author = @import("author.zig");
pub const Factory = *const fn (w.Allocator) anyerror!author.Guest;
// Handles are independent; ABI calls are serialized within this server. A
// handler must honor its Context deadline. The host owns all passed buffers.
pub fn Exports(comptime factory: Factory) type {
    return struct {
        const Slot = struct { id: u64 = 0, guest: ?author.Guest = null, hello: bool = false };
        var slots: [64]Slot = @splat(.{});
        var next: u64 = 0;
        var mutex: std.atomic.Mutex = .unlocked;
        fn lock() void {
            while (!mutex.tryLock()) std.atomic.spinLoopHint();
        }
        pub fn abiVersion() callconv(.c) u32 {
            return 1;
        }
        pub fn open() callconv(.c) u64 {
            lock();
            defer mutex.unlock();
            if (next == std.math.maxInt(u64)) return 0;
            for (&slots) |*s| if (s.guest == null) {
                const guest = factory(std.heap.page_allocator) catch return 0;
                next += 1;
                s.* = .{ .id = next, .guest = guest };
                return next;
            };
            return 0;
        }
        pub fn close(id: u64) callconv(.c) void {
            lock();
            defer mutex.unlock();
            for (&slots) |*s| if (s.id == id and s.guest != null) {
                s.guest.?.deinit();
                s.* = .{};
                return;
            };
        }
        pub fn call(id: u64, op: u32, input: ?[*]const u8, len: u32, out: ?[*]u8, cap: u32, written: ?*u32) callconv(.c) u32 {
            if (written == null) return 1;
            written.?.* = 0;
            if (input == null or out == null or len == 0 or len > w.max_frame or cap != w.max_frame) return 1;
            lock();
            defer mutex.unlock();
            var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
            defer arena.deinit();
            const a = arena.allocator();
            for (&slots) |*s| if (s.id == id and s.guest != null) {
                const result = dispatch(a, s, op, input.?[0..len]) catch return 3;
                if (result.len > w.max_frame) return 1;
                @memcpy(out.?[0..result.len], result);
                written.?.* = @intCast(result.len);
                return 0;
            };
            return 2;
        }
        fn dispatch(a: w.Allocator, s: *Slot, op: u32, bytes: []const u8) ![]u8 {
            const v = try w.decode(a, bytes);
            return switch (op) {
                1 => blk: {
                    const h = try w.typed(struct { deadline: []const u8 }, a, v);
                    if (w.now() >= try w.deadline(h.deadline)) return error.Deadline;
                    s.hello = true;
                    break :blk try w.encode(a, s.guest.?.descriptor);
                },
                2 => blk: {
                    if (!s.hello) return error.Invalid;
                    break :blk try w.encode(a, try s.guest.?.invoke(a, try w.typed(w.Request, a, v)));
                },
                else => error.Unsupported,
            };
        }
    };
}
pub fn exportGuest(comptime factory: Factory) void {
    const E = Exports(factory);
    @export(&E.abiVersion, .{ .name = "ctx_plugin_abi_version" });
    @export(&E.open, .{ .name = "ctx_plugin_open" });
    @export(&E.call, .{ .name = "ctx_plugin_call" });
    @export(&E.close, .{ .name = "ctx_plugin_close" });
}
