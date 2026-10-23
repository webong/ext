// Progress events and reverse calls: the shared fixtures (lib/plugin/testdata/v1), which the Go
// contract computes and every SDK reproduces, and the router behind jsonline.serve, run against a
// scripted host. The fixtures are read from EXT_FIXTURES, or from ../../lib/plugin/testdata/v1 or
// lib/plugin/testdata/v1 (the build root or the repository root, whichever the test runs in).
const std = @import("std");
const w = @import("wire.zig");
const f = @import("frames.zig");
const author = @import("author.zig");
const jsonline = @import("jsonline.zig");
const A = w.Allocator;

const libc = struct {
    const FILE = opaque {};
    extern fn getenv([*c]const u8) ?[*:0]const u8;
    extern fn fopen([*c]const u8, [*c]const u8) ?*FILE;
    extern fn fclose(*FILE) c_int;
    extern fn fread([*c]u8, usize, usize, *FILE) usize;
};
fn readFixture(a: A, name: []const u8) ![]u8 {
    var candidates: [3][]const u8 = undefined;
    var count: usize = 0;
    if (libc.getenv("EXT_FIXTURES")) |dir| {
        candidates[count] = std.mem.span(dir);
        count += 1;
    }
    candidates[count] = "../../lib/plugin/testdata/v1";
    count += 1;
    candidates[count] = "lib/plugin/testdata/v1";
    count += 1;
    for (candidates[0..count]) |dir| {
        const path = try std.fmt.allocPrint(a, "{s}/{s}\x00", .{ dir, name });
        const file = libc.fopen(path.ptr, "rb") orelse continue;
        defer _ = libc.fclose(file);
        var out: std.ArrayList(u8) = .empty;
        var chunk: [65536]u8 = undefined;
        while (true) {
            const got = libc.fread(&chunk, 1, chunk.len, file);
            if (got == 0) break;
            try out.appendSlice(a, chunk[0..got]);
        }
        return out.items;
    }
    return error.FixturesNotFound;
}
fn intOf(v: w.Value) !i64 {
    if (v != .number_string) return error.Invalid;
    return std.fmt.parseInt(i64, v.number_string, 10);
}
fn nameOf(case: w.Value) []const u8 {
    const m = case.object.get("name") orelse return "?";
    if (m != .string) return "?";
    return m.string;
}

fn judge(a: A, section: usize, frame: []const u8, id: []const u8) bool {
    const value = w.decode(a, frame) catch return false;
    switch (section) {
        0 => {
            _ = f.judgeProgress(value) catch return false;
        },
        1 => {
            f.judgeReverseRequest(value) catch return false;
        },
        else => {
            _ = f.judgeReverseResponse(value, id) catch return false;
        },
    }
    return true;
}
test "frames are judged as the Go contract judges them" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const root = try w.decode(a, try readFixture(a, "frame-cases.json"));
    const sections = [_][]const u8{ "progress", "reverseRequest", "reverseResponse" };
    var bad: usize = 0;
    var total: usize = 0;
    for (sections, 0..) |name, section| {
        const cases = (try w.field(root, name)).array.items;
        try std.testing.expect(cases.len >= 20);
        for (cases) |case| {
            const frame = try w.string(try w.field(case, "frame"));
            const id: []const u8 = if (case.object.get("id")) |v| try w.string(v) else "";
            const want = (try w.field(case, "valid")).bool;
            const got = judge(a, section, frame, id);
            total += 1;
            if (got != want) {
                bad += 1;
                if (bad <= 12) std.debug.print("{s}: {s}: accepted {any}\n", .{ name, nameOf(case), got });
            }
        }
    }
    try std.testing.expect(total >= 500);
    try std.testing.expectEqual(@as(usize, 0), bad);
}

test "the rate limiter reproduces every step" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const root = try w.decode(a, try readFixture(a, "progress-limiter-cases.json"));
    const cases = (try w.field(root, "cases")).array.items;
    try std.testing.expect(cases.len >= 10);
    var bad: usize = 0;
    for (cases) |case| {
        var limiter = f.Limiter.init(try intOf(try w.field(case, "eventsPerSecond")), try intOf(try w.field(case, "bytesPerSecond")), 0);
        for ((try w.field(case, "steps")).array.items) |step| {
            const got = limiter.admit(@intCast(try intOf(try w.field(step, "textBytes"))), @as(i128, try intOf(try w.field(step, "atMs"))) * 1_000_000);
            const want = (try w.field(step, "admitted")).bool;
            var want_dropped: u64 = 0;
            if (step.object.get("dropped")) |d| want_dropped = @intCast(try intOf(d));
            if ((got != null) != want or (want and got.? != want_dropped)) {
                bad += 1;
                if (bad <= 12) std.debug.print("limiter: {s}\n", .{nameOf(case)});
            }
        }
        const want_pending: u64 = @intCast(try intOf(try w.field(case, "pending")));
        if (limiter.pending() != want_pending) {
            bad += 1;
            if (bad <= 12) std.debug.print("limiter: {s}: pending\n", .{nameOf(case)});
        }
    }
    try std.testing.expectEqual(@as(usize, 0), bad);
}

fn descriptorAccepted(a: A, raw: []const u8) bool {
    const value = w.decode(a, raw) catch return false;
    const d = w.descriptorFromValue(a, value) catch return false;
    w.validate(d) catch return false;
    return true;
}
const Seen = struct { reporter: bool, events: i64, bytes: i64 };
// What a call gets when the request is admitted: whether it can report progress, at which rate.
fn requestAccepted(a: A, d: w.Descriptor, raw: []const u8) ?Seen {
    const value = w.decode(a, raw) catch return null;
    const r = w.requestFromValue(value) catch return null;
    w.validateRequest(d, r) catch return null;
    const op = w.lookup(d, r.contract, r.operation) catch return null;
    if (r.progress) |rate| {
        if (op.progress) {
            const limits = f.limitsOf(rate);
            return .{ .reporter = true, .events = limits.events_per_second, .bytes = limits.bytes_per_second };
        }
    }
    return .{ .reporter = false, .events = 0, .bytes = 0 };
}
test "the wire model is judged as the Go contract judges it" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const root = try w.decode(a, try readFixture(a, "model-cases.json"));
    var bad: usize = 0;
    const descriptors = (try w.field(root, "descriptors")).array.items;
    try std.testing.expect(descriptors.len > 100);
    for (descriptors) |case| {
        const got = descriptorAccepted(a, try w.string(try w.field(case, "descriptor")));
        if (got != (try w.field(case, "valid")).bool) {
            bad += 1;
            if (bad <= 12) std.debug.print("descriptor: {s}: accepted {any}\n", .{ nameOf(case), got });
        }
    }
    const selected = try w.descriptorFromValue(a, try w.field(root, "requestDescriptor"));
    try w.validate(selected);
    const requests = (try w.field(root, "requests")).array.items;
    try std.testing.expect(requests.len > 100);
    for (requests) |case| {
        const want = (try w.field(case, "valid")).bool;
        const seen = requestAccepted(a, selected, try w.string(try w.field(case, "request")));
        if ((seen != null) != want) {
            bad += 1;
            if (bad <= 12) std.debug.print("request: {s}: accepted {any}\n", .{ nameOf(case), seen != null });
            continue;
        }
        const got = seen orelse continue;
        // The call gets a reporter when its operation declared progress and the request carries a rate;
        // the rate has its defaults filled in.
        var want_reporter = false;
        if (case.object.get("reporter")) |v| want_reporter = v.bool;
        if (got.reporter != want_reporter) {
            bad += 1;
            if (bad <= 12) std.debug.print("request: {s}: reporter\n", .{nameOf(case)});
        } else if (got.reporter) {
            if (got.events != try intOf(try w.field(case, "events")) or got.bytes != try intOf(try w.field(case, "bytes"))) {
                bad += 1;
                if (bad <= 12) std.debug.print("request: {s}: rate\n", .{nameOf(case)});
            }
        }
    }
    try std.testing.expectEqual(@as(usize, 0), bad);
}

test "text is cut on characters" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const raw = try a.alloc(u8, 3002);
    for (0..1500) |i| {
        raw[2 * i] = 0xc3;
        raw[2 * i + 1] = 0xa9;
    }
    raw[3000] = 0xff; // not a character
    raw[3001] = 'x';
    const pieces = try f.splitText(a, raw);
    try std.testing.expectEqual(@as(usize, 2), pieces.len);
    try std.testing.expect(pieces[0].len <= f.max_text and pieces[1].len <= f.max_text);
    try std.testing.expect(std.unicode.utf8ValidateSlice(pieces[0]) and std.unicode.utf8ValidateSlice(pieces[1]));
    try std.testing.expect(w.eq(u8, pieces[1][pieces[1].len - 4 ..], "\xef\xbf\xbdx"));
    try std.testing.expectEqual(@as(usize, 0), (try f.splitText(a, "")).len);
}

// ---- the router, against a scripted host ----

fn allow(_: w.Request) !void {}
fn built(a: A, comptime format: []const u8, args: anytype) !w.Value {
    return w.decode(a, try std.fmt.allocPrint(a, format, args));
}
fn boolText(b: bool) []const u8 {
    return if (b) "true" else "false";
}
const host_ref: w.Ref = .{ .name = "host.svc", .version = "v1" };
fn handle(ctx: *author.Context, r: w.Request, input: w.Value) !w.Value {
    const a = ctx.allocator orelse return error.Unsupported;
    if (w.eq(u8, r.operation, "events")) {
        const first = try ctx.progress(f.Progress.log("a"));
        const second = try ctx.progress(f.Progress.fraction(1, 2));
        return built(a, "{{\"sent\":[{s},{s}]}}", .{ boolText(first == .sent), boolText(second == .sent) });
    }
    if (w.eq(u8, r.operation, "flood")) {
        var dropped: usize = 0;
        for (0..300) |_| {
            if ((try ctx.progress(f.Progress.log("x"))) == .dropped) dropped += 1;
        }
        return built(a, "{{\"dropped\":{d}}}", .{dropped});
    }
    if (w.eq(u8, r.operation, "quiet")) {
        var unsupported = false;
        if (ctx.progress(f.Progress.log("x"))) |_| {} else |e| unsupported = e == error.Unsupported;
        return built(a, "{{\"unsupported\":{s}}}", .{boolText(unsupported)});
    }
    if (w.eq(u8, r.operation, "invalid")) {
        const long = try a.alloc(u8, f.max_text + 1);
        @memset(long, 'x');
        const events = [_]f.Progress{
            .{ .kind = "mystery" },
            f.Progress.fraction(3, 2),
            f.Progress.log(long),
            .{ .stream = "both" },
            .{ .text = "\xff" },
            .{ .kind = "progress", .stream = "out" },
        };
        var refused: usize = 0;
        for (events) |event| {
            if (ctx.progress(event)) |_| {} else |e| {
                if (e == error.Invalid) refused += 1;
            }
        }
        return built(a, "{{\"refused\":{d}}}", .{refused});
    }
    if (w.eq(u8, r.operation, "ask")) {
        const operation = try w.string(try w.field(input, "op"));
        const payload: w.Value = input.object.get("payload") orelse .null;
        const value = ctx.reverse(host_ref, operation, payload) catch |e| {
            if (e == error.Remote) return built(a, "{{\"code\":\"{s}\"}}", .{ctx.reverse_error.?.code});
            if (e == error.Closed) return w.decode(a, "{\"closed\":true}");
            return e;
        };
        var out = w.object();
        try w.put(a, &out, "result", value);
        return out;
    }
    return error.Unsupported;
}
fn testGuest(a: A) !author.Guest {
    var r = try author.Registry.init(a, .{ .id = "zig/frames", .revision = "r1" });
    defer r.deinit();
    inline for (.{
        .{ "events", true },
        .{ "flood", true },
        .{ "invalid", true },
        .{ "quiet", false },
        .{ "ask", false },
    }) |op| {
        try r.register(w.Value, w.Value, .{ .contract = .{ .name = "test.frames", .version = "v1" }, .operation = .{ .name = op[0], .progress = op[1] } }, handle);
    }
    try r.declareReverse(host_ref, &[_]w.Operation{.{ .name = "sign" }});
    return r.guest(allow);
}

const Script = struct {
    input: []const u8,
    pos: usize = 0,
    output: std.ArrayList(u8) = .empty,
    allocator: A,
    fn read(ctx: *anyopaque, bytes: []u8, _: i128) !usize {
        const self: *Script = @ptrCast(@alignCast(ctx));
        const n = @min(bytes.len, self.input.len - self.pos);
        @memcpy(bytes[0..n], self.input[self.pos..][0..n]);
        self.pos += n;
        return n;
    }
    fn write(ctx: *anyopaque, bytes: []const u8, _: i128) !usize {
        const self: *Script = @ptrCast(@alignCast(ctx));
        try self.output.appendSlice(self.allocator, bytes);
        return bytes.len;
    }
    fn close(_: *anyopaque) void {}
};
const Run = struct { frames: []const w.Value, failure: ?anyerror };
// Serves the guest on a script of frames the host sends, and reads what it wrote.
fn run(a: A, guest: *const author.Guest, input: []const u8) !Run {
    var script: Script = .{ .input = input, .allocator = a };
    var conn: jsonline.Connection = .{ .io = .{ .context = &script, .read = Script.read, .write = Script.write, .close = Script.close } };
    var failure: ?anyerror = null;
    jsonline.serve(guest, &conn, a) catch |e| {
        failure = e;
    };
    var frames: std.ArrayList(w.Value) = .empty;
    var lines = std.mem.splitScalar(u8, script.output.items, '\n');
    while (lines.next()) |line| {
        if (line.len == 0) continue;
        try frames.append(a, try w.decode(a, line));
    }
    return .{ .frames = frames.items, .failure = failure };
}
fn helloFrame(a: A) ![]const u8 {
    return std.fmt.allocPrint(a, "{{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\",\"deadline\":\"{s}\"}}\n", .{try w.timestamp(a, w.now() + w.timeout_ns)});
}
fn requestFrame(a: A, id: []const u8, operation: []const u8, payload: []const u8, rate: bool) ![]const u8 {
    const rate_text: []const u8 = if (rate) ",\"progress\":{\"eventsPerSecond\":5}" else "";
    return std.fmt.allocPrint(a, "{{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"{s}\",\"plugin\":{{\"id\":\"zig/frames\",\"revision\":\"r1\"}},\"contract\":{{\"name\":\"test.frames\",\"version\":\"v1\"}},\"operation\":\"{s}\",\"payload\":{s},\"deadline\":\"{s}\"{s}}}\n", .{ id, operation, payload, try w.timestamp(a, w.now() + w.timeout_ns), rate_text });
}
fn join(a: A, parts: []const []const u8) ![]const u8 {
    return std.mem.concat(a, u8, parts);
}
fn textMember(v: w.Value, key: []const u8) []const u8 {
    const m = v.object.get(key) orelse return "";
    if (m != .string) return "";
    return m.string;
}
fn countMember(v: w.Value, key: []const u8) u64 {
    const m = v.object.get(key) orelse return 0;
    if (m != .number_string) return 0;
    return std.fmt.parseInt(u64, m.number_string, 10) catch 0;
}
fn isResponse(v: w.Value, id: []const u8) bool {
    return !v.object.contains("frame") and w.eq(u8, textMember(v, "id"), id);
}

test "events come in order before the response" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    const input = try join(a, &[_][]const u8{ try helloFrame(a), try requestFrame(a, "1", "events", "null", true) });
    const result = try run(a, &guest, input);
    try std.testing.expect(result.failure == null);
    try std.testing.expectEqual(@as(usize, 4), result.frames.len); // hello, two events, the response
    for (result.frames[1..3]) |event| {
        const judged = try f.judgeProgress(event);
        try std.testing.expect(w.eq(u8, judged.parent, "1"));
    }
    try std.testing.expect(w.eq(u8, textMember(result.frames[1], "kind"), "log") and w.eq(u8, textMember(result.frames[1], "text"), "a"));
    try std.testing.expect(w.eq(u8, textMember(result.frames[2], "kind"), "progress") and countMember(result.frames[2], "completed") == 1 and countMember(result.frames[2], "total") == 2);
    try std.testing.expect(isResponse(result.frames[3], "1"));
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(result.frames[3], "payload")), "{\"sent\":[true,true]}"));
}

test "a flood is limited, counted and closed with a marker" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    const input = try join(a, &[_][]const u8{ try helloFrame(a), try requestFrame(a, "1", "flood", "null", true) });
    const result = try run(a, &guest, input);
    try std.testing.expect(result.failure == null);
    const events = result.frames[1 .. result.frames.len - 1];
    const dropped = countMember(try w.field(result.frames[result.frames.len - 1], "payload"), "dropped");
    var delivered: u64 = 0;
    var reported: u64 = 0;
    for (events) |event| {
        if (event.object.contains("text")) delivered += 1;
        reported += countMember(event, "dropped");
    }
    try std.testing.expect(delivered >= 10 and delivered + dropped == 300 and reported == dropped);
    // The drops still unreported when the handler returned are in a marker with only a kind and a count.
    const last = events[events.len - 1];
    try std.testing.expect(!last.object.contains("text") and w.eq(u8, textMember(last, "kind"), "log") and countMember(last, "dropped") > 0);
}

test "an operation that did not declare progress cannot report, and an event that breaks the rules is refused" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    const input = try join(a, &[_][]const u8{ try helloFrame(a), try requestFrame(a, "1", "quiet", "null", true), try requestFrame(a, "2", "invalid", "null", true) });
    const result = try run(a, &guest, input);
    try std.testing.expect(result.failure == null);
    try std.testing.expectEqual(@as(usize, 3), result.frames.len); // hello and two responses: no event was sent
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(result.frames[1], "payload")), "{\"unsupported\":true}"));
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(result.frames[2], "payload")), "{\"refused\":6}"));
}

test "a reverse call names the call and returns the answer" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    const input = try join(a, &[_][]const u8{
        try helloFrame(a),
        try requestFrame(a, "1", "ask", "{\"op\":\"sign\",\"payload\":{\"n\":1}}", false),
        "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"g1\",\"payload\":\"signed\"}\n",
        try requestFrame(a, "2", "ask", "{\"op\":\"sign\"}", false),
        "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"g2\",\"error\":{\"code\":\"denied\",\"message\":\"reverse call refused\"}}\n",
        try requestFrame(a, "3", "ask", "{\"op\":\"nothere\"}", false),
    });
    const result = try run(a, &guest, input);
    try std.testing.expect(result.failure == null);
    try std.testing.expectEqual(@as(usize, 6), result.frames.len); // hello, request, response, request, response, response
    const request = result.frames[1];
    try f.judgeReverseRequest(request);
    try std.testing.expect(w.eq(u8, textMember(request, "id"), "g1") and w.eq(u8, textMember(request, "parent"), "1") and w.eq(u8, textMember(request, "operation"), "sign"));
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(request, "contract")), "{\"name\":\"host.svc\",\"version\":\"v1\"}"));
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(request, "payload")), "{\"n\":1}"));
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(result.frames[2], "payload")), "{\"result\":\"signed\"}"));
    // The host's error comes back with its code, and ids go on counting.
    try std.testing.expect(w.eq(u8, textMember(result.frames[3], "id"), "g2"));
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(result.frames[4], "payload")), "{\"code\":\"denied\"}"));
    // An operation the descriptor does not list is refused without a frame.
    try std.testing.expect(isResponse(result.frames[5], "3"));
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(result.frames[5], "payload")), "{\"code\":\"reverse_undeclared\"}"));
}

test "what the host may not do ends the session" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    const violations = [_][]const u8{
        "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"9\",\"operation\":\"events\"}\n", // a second request during a call
        "{\"frame\":\"mystery\"}\n",
        "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"g99\",\"payload\":1}\n", // never issued
        "{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\",\"parent\":\"1\",\"kind\":\"log\"}\n", // from the host
        "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"g1\",\"payload\":1,\"extra\":1}\n",
        "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"g1\",\"payload\":1,\"error\":{\"code\":\"x\",\"message\":\"m\"}}\n",
        "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"g1\"}\n", // neither payload nor error
        "7\n", // not an object
    };
    for (violations) |bad| {
        const input = try join(a, &[_][]const u8{ try helloFrame(a), try requestFrame(a, "1", "ask", "{\"op\":\"sign\"}", false), bad });
        const result = try run(a, &guest, input);
        try std.testing.expect(result.failure != null and result.failure.? == error.Protocol);
        // The call that was in flight is never answered.
        try std.testing.expectEqual(@as(usize, 2), result.frames.len); // hello and the reverse request
    }
}

test "a connection that ends releases a waiting handler" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    // The host goes away without answering: the call is answered as one cut off, and the session ends.
    const input = try join(a, &[_][]const u8{ try helloFrame(a), try requestFrame(a, "1", "ask", "{\"op\":\"sign\"}", false) });
    const result = try run(a, &guest, input);
    try std.testing.expect(result.failure == null);
    try std.testing.expectEqual(@as(usize, 3), result.frames.len);
    try std.testing.expect(w.eq(u8, try w.encode(a, try w.field(result.frames[2], "payload")), "{\"closed\":true}"));
}

test "a guest invoked directly has no frames" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    const r: w.Request = .{ .apiVersion = w.version, .id = "1", .plugin = guest.descriptor.identity, .contract = .{ .name = "test.frames", .version = "v1" }, .operation = "events", .deadline = try w.timestamp(a, w.now() + w.timeout_ns), .progress = .{} };
    const response = try guest.invoke(a, r); // events needs a reporter
    try std.testing.expect(w.eq(u8, textMember(try w.field(response, "error"), "code"), "operation_failed"));
}

test "the hello may not carry a rate or a parent" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var guest = try testGuest(a);
    defer guest.deinit();
    const stamp = try w.timestamp(a, w.now() + w.timeout_ns);
    const bad = [_][]const u8{ ",\"progress\":{}", ",\"parent\":\"1\"" };
    for (bad) |extra| {
        const input = try std.fmt.allocPrint(a, "{{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\",\"deadline\":\"{s}\"{s}}}\n", .{ stamp, extra });
        const result = try run(a, &guest, input);
        try std.testing.expect(result.failure != null);
        try std.testing.expectEqual(@as(usize, 0), result.frames.len);
    }
}
