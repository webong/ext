// The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
// docs/plugin-guest-frames.md): reports progress and calls its host. It reads inline, so the
// operations that need a reader of its own (idle, collect, burst) answer with the public error
// "unsupported". It serves standard input and output, natively or as a WASI command.
const std = @import("std");
const sdk = @import("ext_plugin");
const w = sdk.wire;
const f = sdk.frames;
const host_ref: w.Ref = .{ .name = "host.svc", .version = "v1" };
const frames_ref: w.Ref = .{ .name = "ext.frames", .version = "v1" };
fn allow(_: w.Request) !void {}
pub fn factory(a: w.Allocator) !sdk.author.Guest {
    var r = try sdk.author.Registry.init(a, .{ .id = "ctx/frames", .revision = "fixture-1" });
    defer r.deinit();
    inline for (.{
        .{ "stream", true },
        .{ "flood", true },
        .{ "limits", true },
        .{ "split", true },
        .{ "plain", false },
        .{ "call", false },
        .{ "twice", false },
        .{ "idle", false },
        .{ "collect", false },
        .{ "burst", false },
    }) |op| {
        try r.register(w.Value, w.Value, .{ .contract = frames_ref, .operation = .{ .name = op[0], .progress = op[1] } }, handle);
    }
    try r.declareReverse(host_ref, &[_]w.Operation{
        .{ .name = "sign", .surface = "signing" },
        .{ .name = "secret" },
        .{ .name = "empty" },
        .{ .name = "fail" },
        .{ .name = "errok" },
        .{ .name = "errretryok" },
        .{ .name = "big" },
        .{ .name = "hold" },
    });
    return r.guest(allow);
}
pub fn main() !void {
    var guest = try factory(std.heap.page_allocator);
    defer guest.deinit();
    var stdio: sdk.jsonline.Stdio = .{};
    var conn = stdio.connection();
    try sdk.jsonline.serve(&guest, &conn, std.heap.page_allocator);
}
// Builds a result from JSON text.
fn built(a: w.Allocator, comptime format: []const u8, args: anytype) !w.Value {
    return w.decode(a, try std.fmt.allocPrint(a, format, args));
}
fn boolText(b: bool) []const u8 {
    return if (b) "true" else "false";
}
fn numberOr(input: w.Value, key: []const u8, fallback: u64) u64 {
    if (input != .object) return fallback;
    const m = input.object.get(key) orelse return fallback;
    if (m != .number_string) return fallback;
    return std.fmt.parseInt(u64, m.number_string, 10) catch fallback;
}
// Reports an event; a refusal is not the handler's failure. True when the rate limit dropped it.
fn report(ctx: *sdk.author.Context, event: f.Progress) bool {
    const result = ctx.progress(event) catch return false;
    return result == .dropped;
}
// How a reverse call came out, in the shape the suite reads.
fn outcome(ctx: *sdk.author.Context, a: w.Allocator, result: anyerror!w.Value) !w.Value {
    var out = w.object();
    if (result) |value| {
        try w.put(a, &out, "result", value);
        return out;
    } else |e| {
        var failure = w.object();
        if (e == error.Remote) {
            const remote = ctx.reverse_error orelse return e;
            try w.put(a, &failure, "code", .{ .string = remote.code });
            try w.put(a, &failure, "message", .{ .string = remote.message });
            if (remote.retryAfterMilliseconds != 0) try w.put(a, &failure, "retry", .{ .integer = remote.retryAfterMilliseconds });
        } else {
            const other: []const u8 = if (e == error.Closed) "closed" else if (e == error.Deadline) "deadline" else if (e == error.Unsupported) "unsupported" else "failed";
            try w.put(a, &failure, "other", .{ .string = other });
        }
        try w.put(a, &out, "error", failure);
        return out;
    }
}
fn handle(ctx: *sdk.author.Context, r: w.Request, input: w.Value) !w.Value {
    const a = ctx.allocator orelse return error.Unsupported;
    if (w.eq(u8, r.operation, "stream")) {
        _ = report(ctx, f.Progress.fraction(1, 3));
        _ = report(ctx, f.Progress.out("hello"));
        _ = report(ctx, f.Progress.err("oops"));
        return w.decode(a, "{\"done\":true}");
    }
    if (w.eq(u8, r.operation, "flood")) {
        const count = numberOr(input, "count", 300);
        var dropped: u64 = 0;
        var i: u64 = 0;
        while (i < count) : (i += 1) {
            if (report(ctx, f.Progress.log("x"))) dropped += 1;
        }
        return built(a, "{{\"dropped\":{d}}}", .{dropped});
    }
    if (w.eq(u8, r.operation, "limits")) {
        if (ctx.progressLimits()) |limits| return built(a, "{{\"declared\":true,\"events\":{d},\"bytes\":{d}}}", .{ limits.events_per_second, limits.bytes_per_second });
        return w.decode(a, "{\"declared\":false,\"events\":0,\"bytes\":0}");
    }
    if (w.eq(u8, r.operation, "split")) {
        const pieces = try f.splitText(a, try w.string(try w.field(input, "text")));
        for (pieces) |piece| {
            _ = report(ctx, f.Progress.out(piece));
        }
        return built(a, "{{\"pieces\":{d}}}", .{pieces.len});
    }
    if (w.eq(u8, r.operation, "plain")) {
        var unsupported = false;
        if (ctx.progress(f.Progress.log("never sent"))) |_| {} else |e| unsupported = e == error.Unsupported;
        return built(a, "{{\"unsupported\":{s},\"limits\":{s}}}", .{ boolText(unsupported), boolText(ctx.progressLimits() != null) });
    }
    if (w.eq(u8, r.operation, "call")) {
        const operation = try w.string(try w.field(input, "op"));
        const payload: w.Value = input.object.get("payload") orelse .null;
        return outcome(ctx, a, ctx.reverse(host_ref, operation, payload));
    }
    if (w.eq(u8, r.operation, "twice")) {
        const first = try ctx.reverse(host_ref, "sign", .{ .integer = 1 });
        const second = try ctx.reverse(host_ref, "sign", .{ .integer = 2 });
        var out = w.object();
        try w.put(a, &out, "first", first);
        try w.put(a, &out, "second", second);
        return out;
    }
    if (w.eq(u8, r.operation, "idle") or w.eq(u8, r.operation, "collect") or w.eq(u8, r.operation, "burst")) {
        return ctx.fail(.{ .code = "unsupported", .message = "not supported here" });
    }
    return error.Unsupported;
}
