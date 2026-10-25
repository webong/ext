const std = @import("std");
const w = @import("wire.zig");
const author = @import("author.zig");
const host = @import("host.zig");
const cap = @import("capability.zig");
const stream = cap.stream;

// The JSON below is what the Go library puts on the wire for the same values; the Rust, Python and
// TypeScript SDKs check the same cases.
fn allow(_: w.Request) !void {}
fn verify(_: w.Descriptor) !void {}

var health_calls: usize = 0;
var updates: usize = 0;
var opened: usize = 0;
var reads: usize = 0;
var closed: usize = 0;
var greedy = false;
const numbers = [_]w.Value{ .{ .integer = 7 }, .{ .integer = 8 } };

fn health(_: *author.Context, _: w.Request, _: cap.HealthRequest) !cap.Health {
    health_calls += 1;
    return .{ .status = "degraded", .ready = false };
}
fn configure(_: *author.Context, _: w.Request, c: cap.Configuration) !cap.Configured {
    updates += 1;
    return .{ .instance = c.instance, .revision = c.revision };
}
fn open(_: *author.Context, _: w.Request, _: stream.OpenRequest) !stream.OpenResponse {
    opened += 1;
    return .{ .id = "s1" };
}
// Two items at most, and the stream ends with the second batch. A read out of sequence is refused
// as Go's service refuses it.
fn read(ctx: *author.Context, _: w.Request, r: stream.ReadRequest) !stream.Batch {
    reads += 1;
    if (r.sequence != reads) return ctx.fail(.{ .code = "sequence", .message = "stream sequence mismatch" });
    const count: usize = if (greedy) 2 else @min(@as(usize, @intCast(r.limit)), 2);
    return .{ .items = numbers[0..count], .done = r.sequence >= 2 };
}
fn close(_: *author.Context, _: w.Request, _: stream.CloseRequest) !stream.Empty {
    closed += 1;
    return .{};
}
fn factory(a: w.Allocator) !author.Guest {
    var r = try author.Registry.init(a, .{ .id = "example/capabilities", .revision = "1" });
    defer r.deinit();
    try r.register(cap.HealthRequest, cap.Health, cap.health_method, health);
    try r.register(cap.Configuration, cap.Configured, cap.configuration_method, configure);
    try r.register(stream.OpenRequest, stream.OpenResponse, stream.open_method, open);
    try r.register(stream.ReadRequest, stream.Batch, stream.read_method, read);
    try r.register(stream.CloseRequest, stream.Empty, stream.close_method, close);
    return r.guest(allow);
}
fn reset() void {
    health_calls = 0;
    updates = 0;
    opened = 0;
    reads = 0;
    closed = 0;
    greedy = false;
}
fn request(a: w.Allocator, g: *const author.Guest, ref: w.Ref, operation: []const u8, payload: []const u8) !w.Request {
    return .{ .apiVersion = w.version, .id = "1", .plugin = g.descriptor.identity, .contract = ref, .operation = operation, .surface = (try w.lookup(g.descriptor, ref, operation)).surface, .deadline = try w.timestamp(a, w.now() + w.timeout_ns), .payload = try w.decode(a, payload) };
}
fn invoke(a: w.Allocator, g: *const author.Guest, ref: w.Ref, operation: []const u8, payload: []const u8) !w.Value {
    return g.invoke(a, try request(a, g, ref, operation, payload));
}
fn payloadOf(a: w.Allocator, response: w.Value) ![]u8 {
    try std.testing.expect(!response.object.contains("error"));
    return w.encode(a, try w.field(response, "payload"));
}
fn errorCode(response: w.Value) []const u8 {
    const err = response.object.get("error") orelse return "";
    return err.object.get("code").?.string;
}

fn repeated(a: w.Allocator, byte: u8, n: usize) ![]u8 {
    const bytes = try a.alloc(u8, n);
    @memset(bytes, byte);
    return bytes;
}

test "methods carry the contracts, operations and surfaces of the Go library" {
    try std.testing.expectEqualStrings("ext.health", cap.health_method.contract.name);
    try std.testing.expectEqualStrings("v1", cap.health_method.contract.version);
    try std.testing.expectEqualStrings("check", cap.health_method.operation.name);
    try std.testing.expectEqualStrings("observation", cap.health_method.operation.surface);
    try std.testing.expectEqualStrings("ext.configuration", cap.configuration_method.contract.name);
    try std.testing.expectEqualStrings("update", cap.configuration_method.operation.name);
    try std.testing.expectEqualStrings("configuration", cap.configuration_method.operation.surface);
    inline for (.{ stream.open_method, stream.read_method, stream.close_method }, .{ "open", "read", "close" }) |method, name| {
        try std.testing.expectEqualStrings("ext.stream", method.contract.name);
        try std.testing.expectEqualStrings("v1", method.contract.version);
        try std.testing.expectEqualStrings(name, method.operation.name);
        try std.testing.expectEqualStrings("", method.operation.surface);
    }
}

test "wire shapes" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    try std.testing.expectEqualStrings("{}", try w.encode(a, cap.HealthRequest{}));
    try std.testing.expectEqualStrings("{}", try w.encode(a, stream.Empty{}));
    try std.testing.expectEqualStrings("{\"status\":\"healthy\",\"ready\":true}", try w.encode(a, cap.Health{ .status = "healthy", .ready = true }));
    try std.testing.expectEqualStrings("{\"instance\":\"one\",\"revision\":\"r1\"}", try w.encode(a, cap.Configured{ .instance = "one", .revision = "r1" }));
    try std.testing.expectEqualStrings("{\"id\":\"s\",\"sequence\":2,\"limit\":3}", try w.encode(a, stream.ReadRequest{ .id = "s", .sequence = 2, .limit = 3 }));
    try std.testing.expectEqualStrings("{\"items\":[],\"done\":true}", try w.encode(a, stream.Batch{ .items = &.{}, .done = true }));
    const configuration = try w.typed(cap.Configuration, a, try w.decode(a, "{\"instance\":\"one\",\"revision\":\"r1\",\"values\":{\"name\":\"a\"}}"));
    try std.testing.expectEqualStrings("{\"instance\":\"one\",\"revision\":\"r1\",\"values\":{\"name\":\"a\"}}", try w.encode(a, configuration));
    // values is any JSON document, null among them, but it is required.
    _ = try w.typed(cap.Configuration, a, try w.decode(a, "{\"instance\":\"one\",\"revision\":\"r1\",\"values\":null}"));
    try std.testing.expectError(error.MissingField, w.typed(cap.Configuration, a, try w.decode(a, "{\"instance\":\"one\",\"revision\":\"r1\"}")));
}

test "health validates like Go" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    for ([_][]const u8{ "healthy", "degraded", "unhealthy" }) |status| try cap.health_method.validate_output(.{ .status = status, .ready = true });
    for ([_][]const u8{ "", "great", "HEALTHY", "healthy " }) |status| {
        try std.testing.expectError(error.Invalid, cap.health_method.validate_output(.{ .status = status, .ready = true }));
    }
    for ([_][]const u8{ "{\"status\":\"healthy\"}", "{\"status\":\"healthy\",\"ready\":true,\"x\":1}", "{\"status\":1,\"ready\":true}", "{\"status\":\"healthy\",\"ready\":1}", "{\"status\":null,\"ready\":true}" }) |raw| {
        if (w.typed(cap.Health, a, try w.decode(a, raw))) |_| return error.AcceptedMalformedHealth else |_| {}
    }
    _ = try w.typed(cap.HealthRequest, a, try w.decode(a, "{}"));
    for ([_][]const u8{ "{\"x\":1}", "null", "[]" }) |raw| {
        if (w.typed(cap.HealthRequest, a, try w.decode(a, raw))) |_| return error.AcceptedMalformedHealthRequest else |_| {}
    }
}

test "configuration validates like Go" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const values: w.Value = .{ .string = "a" };
    const check = cap.configuration_method.validate_input;
    try check(.{ .instance = "one", .revision = "r1", .values = values });
    try check(.{ .instance = try repeated(a, 'x', 256), .revision = try repeated(a, 'r', 256), .values = values });
    try std.testing.expectError(error.Invalid, check(.{ .instance = "", .revision = "r1", .values = values }));
    try std.testing.expectError(error.Invalid, check(.{ .instance = "one", .revision = "", .values = values }));
    try std.testing.expectError(error.Invalid, check(.{ .instance = try repeated(a, 'x', 257), .revision = "r1", .values = values }));
    try std.testing.expectError(error.Invalid, check(.{ .instance = "one", .revision = try repeated(a, 'r', 257), .values = values }));
    for ([_][]const u8{
        "{\"instance\":\"one\",\"revision\":\"r1\"}",
        "{\"instance\":\"one\",\"revision\":\"r1\",\"values\":{},\"extra\":1}",
        "{\"instance\":1,\"revision\":\"r1\",\"values\":{}}",
        "{\"instance\":\"one\",\"revision\":null,\"values\":{}}",
        "null",
        "[]",
        "\"x\"",
    }) |raw| {
        if (w.typed(cap.Configuration, a, try w.decode(a, raw))) |_| return error.AcceptedMalformedConfiguration else |_| {}
    }
}

test "stream requests validate like Go" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const check = stream.read_method.validate_input;
    try check(.{ .id = "s1", .sequence = 1, .limit = 1 });
    try check(.{ .id = try repeated(a, 'i', 64), .sequence = 7, .limit = stream.max_items });
    for ([_]stream.ReadRequest{
        .{ .id = "", .sequence = 1, .limit = 1 },
        .{ .id = try repeated(a, 'i', 65), .sequence = 1, .limit = 1 },
        .{ .id = "s1", .sequence = 0, .limit = 1 },
        .{ .id = "s1", .sequence = 1, .limit = 0 },
        .{ .id = "s1", .sequence = 1, .limit = -1 },
        .{ .id = "s1", .sequence = 1, .limit = stream.max_items + 1 },
    }) |bad| try std.testing.expectError(error.Invalid, check(bad));
    try stream.close_method.validate_input(.{ .id = "s" });
    try std.testing.expectError(error.Invalid, stream.close_method.validate_input(.{ .id = "" }));
    try std.testing.expectError(error.Invalid, stream.close_method.validate_input(.{ .id = try repeated(a, 'i', 65) }));
    try stream.open_method.validate_output(.{ .id = "s" });
    try std.testing.expectError(error.Invalid, stream.open_method.validate_output(.{ .id = "" }));
    try std.testing.expectError(error.Invalid, stream.open_method.validate_output(.{ .id = try repeated(a, 'i', 65) }));
}

test "stream read numbers are whole numbers, as in Go" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const r = try w.typed(stream.ReadRequest, a, try w.decode(a, "{\"id\":\"s1\",\"sequence\":2,\"limit\":3}"));
    try std.testing.expectEqualStrings("s1", r.id);
    try std.testing.expectEqual(@as(u64, 2), r.sequence);
    try std.testing.expectEqual(@as(i64, 3), r.limit);
    const big = try w.typed(stream.ReadRequest, a, try w.decode(a, "{\"id\":\"s1\",\"sequence\":18446744073709551615,\"limit\":-9223372036854775808}"));
    try std.testing.expectEqual(std.math.maxInt(u64), big.sequence);
    try std.testing.expectEqual(std.math.minInt(i64), big.limit);
    for ([_][]const u8{
        "{\"id\":\"s1\",\"sequence\":1.0,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":1e0,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":1,\"limit\":1.5}",
        "{\"id\":\"s1\",\"sequence\":1,\"limit\":1E0}",
        "{\"id\":\"s1\",\"sequence\":-1,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":18446744073709551616,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":1,\"limit\":9223372036854775808}",
        "{\"id\":\"s1\",\"sequence\":true,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":\"1\",\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":null,\"limit\":1}",
        "{\"id\":\"s1\",\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":1}",
        "{\"sequence\":1,\"limit\":1}",
        "{\"id\":1,\"sequence\":1,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":1,\"limit\":1,\"extra\":1}",
        "null",
        "[]",
        "\"x\"",
    }) |raw| {
        if (w.typed(stream.ReadRequest, a, try w.decode(a, raw))) |_| return error.AcceptedMalformedRead else |_| {}
    }
}

test "batch limits match Go" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var items: [stream.max_items + 1]w.Value = undefined;
    for (&items, 0..) |*item, i| item.* = .{ .integer = @intCast(i) };
    try stream.validateBatch(.{ .items = items[0..stream.max_items], .done = false });
    try std.testing.expectError(error.Invalid, stream.validateBatch(.{ .items = items[0 .. stream.max_items + 1], .done = false }));
    try stream.validateBatch(.{ .items = &.{}, .done = true });
    // Two items of 600 KiB are more than the 1 MiB a batch may hold, though each fits alone.
    const big: w.Value = .{ .string = try repeated(a, 'x', 600 << 10) };
    try stream.validateBatch(.{ .items = &.{big}, .done = false });
    try std.testing.expectError(error.Invalid, stream.validateBatch(.{ .items = &.{ big, big }, .done = false }));
    // The size is that of the compact JSON: a string of exactly the limit does not fit once quoted.
    const exact: w.Value = .{ .string = try repeated(a, 'x', stream.max_batch_bytes - 2) };
    try stream.validateBatch(.{ .items = &.{exact}, .done = false });
    const over: w.Value = .{ .string = try repeated(a, 'x', stream.max_batch_bytes - 1) };
    try std.testing.expectError(error.Invalid, stream.validateBatch(.{ .items = &.{over}, .done = false }));
    for ([_][]const u8{ "{\"items\":null,\"done\":false}", "{\"items\":[]}", "{\"items\":[],\"done\":1}", "{\"items\":{},\"done\":false}", "{\"items\":[],\"done\":false,\"x\":1}" }) |raw| {
        if (w.typed(stream.Batch, a, try w.decode(a, raw))) |_| return error.AcceptedMalformedBatch else |_| {}
    }
}

test "a guest serves the capability contracts" {
    reset();
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var g = try factory(std.testing.allocator);
    defer g.deinit();
    try w.validate(g.descriptor);
    const health_ref = cap.health_method.contract;
    const config_ref = cap.configuration_method.contract;
    const stream_ref = stream.open_method.contract;

    try std.testing.expectEqualStrings("{\"status\":\"degraded\",\"ready\":false}", try payloadOf(a, try invoke(a, &g, health_ref, "check", "{}")));
    try std.testing.expectEqualStrings("invalid_input", errorCode(try invoke(a, &g, health_ref, "check", "{\"x\":1}")));
    try std.testing.expectEqual(@as(usize, 1), health_calls);

    try std.testing.expectEqualStrings("{\"instance\":\"one\",\"revision\":\"r1\"}", try payloadOf(a, try invoke(a, &g, config_ref, "update", "{\"instance\":\"one\",\"revision\":\"r1\",\"values\":{\"name\":\"a\"}}")));
    try std.testing.expectEqual(@as(usize, 1), updates);
    const long_instance = try std.fmt.allocPrint(a, "{{\"instance\":\"{s}\",\"revision\":\"{s}\",\"values\":1}}", .{ try repeated(a, 'x', 256), try repeated(a, 'r', 256) });
    _ = try payloadOf(a, try invoke(a, &g, config_ref, "update", long_instance));
    try std.testing.expectEqual(@as(usize, 2), updates);
    // None of these reaches the handler.
    const too_long = try std.fmt.allocPrint(a, "{{\"instance\":\"{s}\",\"revision\":\"r1\",\"values\":1}}", .{try repeated(a, 'x', 257)});
    for ([_][]const u8{
        "{\"instance\":\"\",\"revision\":\"r1\",\"values\":{}}",
        "{\"instance\":\"one\",\"revision\":\"\",\"values\":{}}",
        "{\"instance\":\"one\",\"revision\":\"r1\"}",
        "{\"instance\":\"one\",\"revision\":\"r1\",\"values\":{},\"extra\":1}",
        "{\"instance\":1,\"revision\":\"r1\",\"values\":{}}",
        too_long,
    }) |bad| {
        try std.testing.expectEqualStrings("invalid_input", errorCode(try invoke(a, &g, config_ref, "update", bad)));
    }
    try std.testing.expectEqual(@as(usize, 2), updates);

    try std.testing.expectEqualStrings("{\"id\":\"s1\"}", try payloadOf(a, try invoke(a, &g, stream_ref, "open", "{\"parameters\":{}}")));
    try std.testing.expectEqualStrings("invalid_input", errorCode(try invoke(a, &g, stream_ref, "open", "{}")));
    try std.testing.expectEqualStrings("{\"items\":[7,8],\"done\":false}", try payloadOf(a, try invoke(a, &g, stream_ref, "read", "{\"id\":\"s1\",\"sequence\":1,\"limit\":5}")));
    try std.testing.expectEqualStrings("{\"items\":[7],\"done\":true}", try payloadOf(a, try invoke(a, &g, stream_ref, "read", "{\"id\":\"s1\",\"sequence\":2,\"limit\":1}")));
    try std.testing.expectEqual(@as(usize, 2), reads);
    for ([_][]const u8{
        "{\"id\":\"\",\"sequence\":1,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":0,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":1,\"limit\":0}",
        "{\"id\":\"s1\",\"sequence\":1,\"limit\":-1}",
        "{\"id\":\"s1\",\"sequence\":1,\"limit\":257}",
        "{\"id\":\"s1\",\"sequence\":1.0,\"limit\":1}",
        "{\"id\":\"s1\",\"sequence\":true,\"limit\":1}",
    }) |bad| {
        try std.testing.expectEqualStrings("invalid_input", errorCode(try invoke(a, &g, stream_ref, "read", bad)));
    }
    try std.testing.expectEqual(@as(usize, 2), reads);
    try std.testing.expectEqualStrings("{}", try payloadOf(a, try invoke(a, &g, stream_ref, "close", "{\"id\":\"s1\"}")));
    try std.testing.expectEqualStrings("invalid_input", errorCode(try invoke(a, &g, stream_ref, "close", "{\"id\":\"\"}")));
    try std.testing.expectEqual(@as(usize, 1), closed);
}

fn brokenHealth(_: *author.Context, _: w.Request, _: cap.HealthRequest) !cap.Health {
    return .{ .status = "great", .ready = true };
}
fn brokenOpen(_: *author.Context, _: w.Request, _: stream.OpenRequest) !stream.OpenResponse {
    return .{ .id = "" };
}
const too_many = blk: {
    var items: [stream.max_items + 1]w.Value = undefined;
    for (&items) |*item| item.* = .{ .integer = 1 };
    break :blk items;
};
fn brokenRead(_: *author.Context, _: w.Request, _: stream.ReadRequest) !stream.Batch {
    return .{ .items = &too_many, .done = false };
}

test "a handler that breaks the output rules is not answered" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var r = try author.Registry.init(std.testing.allocator, .{ .id = "example/broken", .revision = "1" });
    defer r.deinit();
    try r.register(cap.HealthRequest, cap.Health, cap.health_method, brokenHealth);
    try r.register(stream.OpenRequest, stream.OpenResponse, stream.open_method, brokenOpen);
    try r.register(stream.ReadRequest, stream.Batch, stream.read_method, brokenRead);
    var g = try r.guest(allow);
    defer g.deinit();
    try std.testing.expectEqualStrings("operation_failed", errorCode(try invoke(a, &g, cap.health_method.contract, "check", "{}")));
    try std.testing.expectEqualStrings("operation_failed", errorCode(try invoke(a, &g, stream.open_method.contract, "open", "{\"parameters\":null}")));
    try std.testing.expectEqualStrings("operation_failed", errorCode(try invoke(a, &g, stream.read_method.contract, "read", "{\"id\":\"s\",\"sequence\":1,\"limit\":1}")));
}

const Local = struct {
    guest: *const author.Guest,
    calls: usize = 0,
    fn connect(ctx: *anyopaque) !host.Backend {
        return .{ .context = ctx, .exchange = exchange, .close = disconnect };
    }
    fn disconnect(_: *anyopaque) void {}
    fn exchange(ctx: *anyopaque, a: w.Allocator, op: u32, bytes: []const u8, _: i128) ![]u8 {
        const self: *Local = @ptrCast(@alignCast(ctx));
        if (op == 1) return w.encode(a, self.guest.descriptor);
        self.calls += 1;
        return w.encode(a, try self.guest.invoke(a, try w.requestFromValue(try w.decode(a, bytes))));
    }
};

test "a host pulls a stream and calls health and configuration" {
    reset();
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var g = try factory(std.testing.allocator);
    defer g.deinit();
    var local: Local = .{ .guest = &g };
    var session = try host.Session.open(a, g.descriptor, verify, .{ .context = &local, .connect = Local.connect }, allow);
    defer session.deinit();

    const h = (try session.call(cap.HealthRequest, cap.Health, a, cap.health_method, .{})).value;
    try std.testing.expectEqualStrings("degraded", h.status);
    try std.testing.expect(!h.ready);
    const configured = (try session.call(cap.Configuration, cap.Configured, a, cap.configuration_method, .{ .instance = "one", .revision = "r1", .values = .{ .string = "v" } })).value;
    try std.testing.expectEqualStrings("one", configured.instance);
    try std.testing.expectEqualStrings("r1", configured.revision);
    try std.testing.expectError(error.Invalid, session.call(cap.Configuration, cap.Configured, a, cap.configuration_method, .{ .instance = "", .revision = "r1", .values = .null }));
    try std.testing.expectEqual(@as(usize, 2), local.calls);

    var client = (try stream.Client.open(&session, a, .null)).value;
    try std.testing.expectEqualStrings("s1", client.id());
    const first = (try client.read(a, 5)).value;
    try std.testing.expectEqual(@as(usize, 2), first.items.len);
    try std.testing.expect(!first.done);
    try std.testing.expectEqualStrings("7", first.items[0].number_string);
    const second = (try client.read(a, 1)).value;
    try std.testing.expectEqual(@as(usize, 1), second.items.len);
    try std.testing.expect(second.done);
    // A finished stream answers locally, with an empty final batch.
    const calls = local.calls;
    const after = (try client.read(a, 1)).value;
    try std.testing.expectEqual(@as(usize, 0), after.items.len);
    try std.testing.expect(after.done);
    try std.testing.expectEqual(calls, local.calls);
    switch (try client.close(a)) {
        .value => {},
        .remote => return error.UnexpectedRemote,
    }
    try std.testing.expectEqual(@as(usize, 1), closed);
    try std.testing.expectEqual(@as(usize, 2), reads);
}

test "a failed read ends the stream and nothing is retried" {
    reset();
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var g = try factory(std.testing.allocator);
    defer g.deinit();
    var local: Local = .{ .guest = &g };
    var session = try host.Session.open(a, g.descriptor, verify, .{ .context = &local, .connect = Local.connect }, allow);
    defer session.deinit();

    // A limit out of range never leaves the host, and does not end the stream.
    var client = (try stream.Client.open(&session, a, .null)).value;
    const calls = local.calls;
    try std.testing.expectError(error.Invalid, client.read(a, 0));
    try std.testing.expectError(error.Invalid, client.read(a, stream.max_items + 1));
    try std.testing.expectEqual(calls, local.calls);
    try std.testing.expect(!client.done);

    // The guest refuses a read that is out of sequence; the client keeps the guest's code and stops.
    reads = 5;
    switch (try client.read(a, 1)) {
        .remote => |e| try std.testing.expectEqualStrings("sequence", e.code),
        .value => return error.ExpectedRemote,
    }
    try std.testing.expect(client.done);
    const seen = local.calls;
    try std.testing.expect((try client.read(a, 1)).value.done);
    try std.testing.expectEqual(seen, local.calls);

    // A guest that returns more items than were asked for is a protocol error, and ends the stream.
    reads = 0;
    greedy = true;
    var other = (try stream.Client.open(&session, a, .null)).value;
    try std.testing.expectError(error.Invalid, other.read(a, 1));
    try std.testing.expect(other.done);
}
