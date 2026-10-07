const std = @import("std");
const w = @import("wire.zig");
const author = @import("author.zig");
const cabi = @import("cabi.zig");
const jsonline = @import("jsonline.zig");
const host = @import("host.zig");
fn allow(_: w.Request) !void {}
const Message = struct { text: []const u8 };
fn check(m: Message) !void {
    if (m.text.len > 8) return error.Invalid;
}
fn echo(_: *author.Context, _: w.Request, m: Message) !Message {
    return m;
}
fn factory(a: w.Allocator) !author.Guest {
    var r = try author.Registry.init(a, .{ .id = "example/typed", .revision = "1" });
    defer r.deinit();
    try r.register(Message, Message, .{ .contract = .{ .name = "example.echo", .version = "v1" }, .operation = .{ .name = "echo" }, .validate_input = check }, echo);
    return r.guest(allow);
}
test "strict decoding, deadlines, typed guest snapshot" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    for ([_][]const u8{ "{\"x\":1,\"x\":2}", "{\"x\":1,\"\\u0078\":2}", "{} {}", "[1,]", "\xff" }) |raw| {
        if (w.decode(a, raw)) |_| return error.AcceptedMalformedJSON else |_| {}
    }
    try std.testing.expectEqual(try w.deadline("2026-10-04T00:00:00Z"), try w.deadline("2026-10-04T01:00:00+01:00"));
    try std.testing.expectError(error.Invalid, w.deadline("2026-02-30T00:00:00Z"));
    const now = w.now();
    const stamp = try w.timestamp(a, now);
    try std.testing.expectEqual(now, try w.deadline(stamp));
    var g = try factory(std.testing.allocator);
    defer g.deinit();
    try w.validate(g.descriptor);
    var r: w.Request = .{ .apiVersion = w.version, .id = "1", .plugin = g.descriptor.identity, .contract = .{ .name = "example.echo", .version = "v1" }, .operation = "echo", .deadline = try w.timestamp(a, now + w.timeout_ns), .payload = try w.decode(a, "{\"text\":\"hello\"}") };
    const response = try g.invoke(a, r);
    try w.validateResponse(a, response, "1");
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(response, "payload")), "{\"text\":\"hello\"}"));
    r.payload = try w.decode(a, "{\"text\":\"too long input\"}");
    const bad = try g.invoke(a, r);
    try std.testing.expect(bad.object.contains("error"));
    r.payload = try w.decode(a, "{\"text\":\"ok\",\"unknown\":true}");
    try std.testing.expect((try g.invoke(a, r)).object.contains("error"));
}
test "C ABI handle ownership and malformed buffer rejection" {
    const E = cabi.Exports(factory);
    const id = E.open();
    try std.testing.expect(id != 0);
    var size: u32 = 99;
    try std.testing.expectEqual(@as(u32, 1), E.call(id, 1, null, 0, null, 0, &size));
    try std.testing.expectEqual(@as(u32, 0), size);
    E.close(id);
    E.close(id);
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const hello = try w.encode(a, .{ .deadline = try w.timestamp(a, w.now() + w.timeout_ns) });
    const out = try a.alloc(u8, w.max_frame);
    try std.testing.expectEqual(@as(u32, 2), E.call(id, 1, hello.ptr, @intCast(hello.len), out.ptr, w.max_frame, &size));
    const live = E.open();
    defer E.close(live);
    try std.testing.expect(live != id);
    try std.testing.expectEqual(@as(u32, 0), E.call(live, 1, hello.ptr, @intCast(hello.len), out.ptr, w.max_frame, &size));
    try w.validate(try w.typed(w.Descriptor, a, try w.decode(a, out[0..size])));
}

const Local = struct {
    guest: *author.Guest,
    connected: usize = 0,
    closed: usize = 0,
    corrupt: bool = false,
    fn connect(ctx: *anyopaque) !host.Backend {
        const self: *Local = @ptrCast(@alignCast(ctx));
        self.connected += 1;
        return .{ .context = self, .exchange = exchange, .close = close };
    }
    fn close(ctx: *anyopaque) void {
        const self: *Local = @ptrCast(@alignCast(ctx));
        self.closed += 1;
    }
    fn exchange(ctx: *anyopaque, a: w.Allocator, op: u32, bytes: []const u8, _: i128) ![]u8 {
        const self: *Local = @ptrCast(@alignCast(ctx));
        if (op == 1) return w.encode(a, self.guest.descriptor);
        if (self.corrupt) return a.dupe(u8, "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"wrong\",\"payload\":null}");
        return w.encode(a, try self.guest.invoke(a, try w.typed(w.Request, a, try w.decode(a, bytes))));
    }
};
fn verify(_: w.Descriptor) !void {}
fn denyVerify(_: w.Descriptor) !void {
    return error.Denied;
}
fn deny(_: w.Request) !void {
    return error.Denied;
}
test "host verification before connect, call policy, malformed response cleanup" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try factory(std.testing.allocator);
    defer guest.deinit();
    var local: Local = .{ .guest = &guest };
    const connector: host.Connector = .{ .context = &local, .connect = Local.connect };
    try std.testing.expectError(error.Denied, host.Session.open(a, guest.descriptor, denyVerify, connector, allow));
    try std.testing.expectEqual(@as(usize, 0), local.connected);
    var denied = try host.Session.open(a, guest.descriptor, verify, connector, deny);
    defer denied.deinit();
    try std.testing.expectError(error.Denied, denied.callRaw(a, .{ .name = "example.echo", .version = "v1" }, "echo", .null, w.timeout_ns));
    try std.testing.expectEqual(@as(usize, 0), local.closed);
    denied.close();
    var session = try host.Session.open(a, guest.descriptor, verify, connector, allow);
    defer session.deinit();
    const method: author.Method(Message, Message) = .{ .contract = .{ .name = "example.echo", .version = "v1" }, .operation = .{ .name = "echo" }, .validate_input = check };
    const result = try session.call(Message, Message, a, method, .{ .text = "hello" });
    try std.testing.expect(w.eq(u8, result.value.text, "hello"));
    local.corrupt = true;
    try std.testing.expectError(error.Mismatch, session.call(Message, Message, a, method, .{ .text = "hello" }));
    try std.testing.expect(session.closed);
    session.close();
    try std.testing.expectEqual(@as(usize, 2), local.closed);
}
const Fragmented = struct {
    input: []const u8,
    pos: usize = 0,
    closed: bool = false,
    fn read(ctx: *anyopaque, bytes: []u8, _: i128) !usize {
        const self: *Fragmented = @ptrCast(@alignCast(ctx));
        const n = @min(@min(bytes.len, 2), self.input.len - self.pos);
        @memcpy(bytes[0..n], self.input[self.pos..][0..n]);
        self.pos += n;
        return n;
    }
    fn write(_: *anyopaque, bytes: []const u8, _: i128) !usize {
        return bytes.len;
    }
    fn close(ctx: *anyopaque) void {
        const self: *Fragmented = @ptrCast(@alignCast(ctx));
        self.closed = true;
    }
};
test "JSON line fragmentation, EOF and truncated frame" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var input: Fragmented = .{ .input = "one\ntwo\n" };
    var conn: jsonline.Connection = .{ .io = .{ .context = &input, .read = Fragmented.read, .write = Fragmented.write, .close = Fragmented.close } };
    try std.testing.expect(w.eq(u8, (try conn.readFrame(a, w.now() + w.timeout_ns)).?, "one"));
    try std.testing.expect(w.eq(u8, (try conn.readFrame(a, w.now() + w.timeout_ns)).?, "two"));
    try std.testing.expect((try conn.readFrame(a, w.now() + w.timeout_ns)) == null);
    conn.close();
    try std.testing.expect(input.closed);
    input = .{ .input = "{}" };
    conn.closed = false;
    try std.testing.expectError(error.Truncated, conn.readFrame(a, w.now() + w.timeout_ns));
}
