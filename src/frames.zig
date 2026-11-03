// Progress events and reverse calls for a guest (docs/plugin-guest-frames.md). The rules are the Go
// contract's, pinned by lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases}.json.
//
// This guest reads inline: it has no reader of its own, so the thread that waits for the host's
// answer to a reverse call reads the connection itself, which suffices because the host sends
// nothing but that answer during a call. A call therefore makes one reverse call at a time and has
// no session-scoped caller, and the wait ends with the answer or the end of the connection.
const std = @import("std");
const w = @import("wire.zig");
const A = w.Allocator;

pub const max_text = 2048;
pub const max_retry: u64 = 365 * 24 * 60 * 60 * 1000;
pub const default_events: i64 = 50;
pub const default_bytes: i64 = 65536;

// One progress event: a progress count of completed out of total (zero when not known), or a log
// line, optionally on a stream (out or err). Text is at most 2048 bytes of UTF-8; splitText cuts a
// longer line.
pub const Progress = struct {
    kind: []const u8 = "log",
    stream: []const u8 = "",
    text: []const u8 = "",
    completed: u64 = 0,
    total: u64 = 0,
    pub fn log(text: []const u8) Progress {
        return .{ .text = text };
    }
    // A log line of a program's standard output.
    pub fn out(text: []const u8) Progress {
        return .{ .stream = "out", .text = text };
    }
    // A log line of a program's standard error.
    pub fn err(text: []const u8) Progress {
        return .{ .stream = "err", .text = text };
    }
    pub fn fraction(completed: u64, total: u64) Progress {
        return .{ .kind = "progress", .completed = completed, .total = total };
    }
};
// What became of an event: written, or dropped by the rate limit (routine; the next event that
// passes carries the count).
pub const Outcome = enum { sent, dropped };
pub const Limits = struct { events_per_second: i64, bytes_per_second: i64 };
// An event as it travels in a frame, with the count of events dropped before it.
pub const Event = struct { kind: []const u8, stream: []const u8, text: []const u8, completed: u64, total: u64, dropped: u64 };
pub const ProgressFrame = struct { parent: []const u8, event: Event };
pub const Answer = union(enum) { payload: w.Value, remote: w.RemoteError };

pub fn limitsOf(rate: w.Rate) Limits {
    return .{
        .events_per_second = if (rate.eventsPerSecond > 0) rate.eventsPerSecond else default_events,
        .bytes_per_second = if (rate.bytesPerSecond > 0) rate.bytesPerSecond else default_bytes,
    };
}

// The length of the character a byte starts, or 0 for a byte that starts none.
fn sequenceLength(b: u8) usize {
    if (b < 0x80) return 1;
    if (b >= 0xC2 and b <= 0xDF) return 2;
    if (b >= 0xE0 and b <= 0xEF) return 3;
    if (b >= 0xF0 and b <= 0xF4) return 4;
    return 0;
}

// Cuts text into pieces that each fit one event: at most 2048 bytes, never ending inside a
// character. A byte that does not belong to a character becomes U+FFFD.
pub fn splitText(a: A, text: []const u8) ![]const []const u8 {
    var clean: std.ArrayList(u8) = .empty;
    var i: usize = 0;
    while (i < text.len) {
        const n = sequenceLength(text[i]);
        if (n != 0 and i + n <= text.len and std.unicode.utf8ValidateSlice(text[i .. i + n])) {
            try clean.appendSlice(a, text[i .. i + n]);
            i += n;
        } else {
            try clean.appendSlice(a, "\xef\xbf\xbd");
            i += 1;
        }
    }
    var pieces: std.ArrayList([]const u8) = .empty;
    var start: usize = 0;
    while (start < clean.items.len) {
        var end = @min(start + max_text, clean.items.len);
        if (end < clean.items.len) {
            while (end > start and (clean.items[end] & 0xC0) == 0x80) end -= 1;
        }
        try pieces.append(a, clean.items[start..end]);
        start = end;
    }
    return pieces.items;
}

// The sender's rate limit: a token bucket counted in events and bytes of text with a burst of twice
// the rate, and a count of events dropped since the last one that passed. Time is a clock in
// nanoseconds, given by the caller.
pub const Limiter = struct {
    events: f64,
    bytes: f64,
    event_rate: f64,
    byte_rate: f64,
    last: i128,
    dropped: u64 = 0,
    pub fn init(events_per_second: i64, bytes_per_second: i64, now_ns: i128) Limiter {
        const event_rate: f64 = @floatFromInt(if (events_per_second > 0) events_per_second else default_events);
        const byte_rate: f64 = @floatFromInt(if (bytes_per_second > 0) bytes_per_second else default_bytes);
        return .{ .events = 2 * event_rate, .bytes = 2 * byte_rate, .event_rate = event_rate, .byte_rate = byte_rate, .last = now_ns };
    }
    // The number of events dropped since the previous one that passed (which the caller attaches to
    // this event), or null when the rate limit drops it.
    pub fn admit(self: *Limiter, text_bytes: usize, now_ns: i128) ?u64 {
        const delta = now_ns - self.last;
        // Seconds as Go's Duration.Seconds computes them.
        const whole = @divTrunc(delta, 1_000_000_000);
        const rest = delta - whole * 1_000_000_000;
        const elapsed = @as(f64, @floatFromInt(whole)) + @as(f64, @floatFromInt(rest)) / 1e9;
        if (elapsed > 0) {
            self.events = @min(self.events + elapsed * self.event_rate, 2 * self.event_rate);
            self.bytes = @min(self.bytes + elapsed * self.byte_rate, 2 * self.byte_rate);
            self.last = now_ns;
        }
        const size: f64 = @floatFromInt(text_bytes);
        if (self.events < 1 or self.bytes < size) {
            self.dropped += 1;
            return null;
        }
        self.events -= 1;
        self.bytes -= size;
        const reported = self.dropped;
        self.dropped = 0;
        return reported;
    }
    // The drops not yet reported, reset to zero.
    pub fn pending(self: *Limiter) u64 {
        const reported = self.dropped;
        self.dropped = 0;
        return reported;
    }
};

// ---- judging frames; every refusal is error.Protocol ----

fn strict(v: w.Value, allowed: []const []const u8) !void {
    if (v != .object) return error.Protocol;
    for (v.object.keys()) |k| {
        var known = false;
        for (allowed) |name| {
            if (w.eq(u8, k, name)) known = true;
        }
        if (!known) return error.Protocol;
    }
}
// A member Go decodes into a string: absent and null are empty.
fn textOf(v: w.Value, key: []const u8) ![]const u8 {
    const m = v.object.get(key) orelse return "";
    return switch (m) {
        .null => "",
        .string => |s| s,
        else => error.Protocol,
    };
}
// A member Go decodes into a uint64: absent and null are zero, and anything but a whole number that
// fits is refused (1.0 and 1e2 are not whole numbers to Go).
fn countOf(v: w.Value, key: []const u8) !u64 {
    const m = v.object.get(key) orelse return 0;
    switch (m) {
        .null => return 0,
        .number_string => |s| {
            if (s.len == 0 or (s.len > 1 and s[0] == '0')) return error.Protocol;
            for (s) |b| {
                if (!std.ascii.isDigit(b)) return error.Protocol;
            }
            return std.fmt.parseInt(u64, s, 10) catch return error.Protocol;
        },
        else => return error.Protocol,
    }
}
pub fn checkEvent(e: Event) !void {
    const kind_ok = w.eq(u8, e.kind, "progress") or w.eq(u8, e.kind, "log");
    const stream_ok = e.stream.len == 0 or w.eq(u8, e.stream, "out") or w.eq(u8, e.stream, "err");
    if (!kind_ok or !stream_ok or (e.stream.len != 0 and !w.eq(u8, e.kind, "log"))) return error.Protocol;
    if (e.text.len > max_text or !std.unicode.utf8ValidateSlice(e.text) or (e.total != 0 and e.completed > e.total)) return error.Protocol;
}
const progress_fields = [_][]const u8{ "frame", "apiVersion", "parent", "kind", "stream", "text", "completed", "total", "dropped" };
// Judges a progress frame: its parent (the call it belongs to) and the event.
pub fn judgeProgress(v: w.Value) !ProgressFrame {
    try strict(v, &progress_fields);
    const parent = try textOf(v, "parent");
    const event: Event = .{
        .kind = try textOf(v, "kind"),
        .stream = try textOf(v, "stream"),
        .text = try textOf(v, "text"),
        .completed = try countOf(v, "completed"),
        .total = try countOf(v, "total"),
        .dropped = try countOf(v, "dropped"),
    };
    if (!w.eq(u8, try textOf(v, "frame"), "progress") or !w.eq(u8, try textOf(v, "apiVersion"), w.version) or !w.identifier(parent, true)) return error.Protocol;
    try checkEvent(event);
    return .{ .parent = parent, .event = event };
}
const request_fields = [_][]const u8{ "frame", "apiVersion", "id", "parent", "contract", "operation", "deadline", "payload" };
const ref_fields = [_][]const u8{ "name", "version" };
// Judges a reverse request frame, as the host reads it.
pub fn judgeReverseRequest(v: w.Value) !void {
    try strict(v, &request_fields);
    const ref = v.object.get("contract") orelse return error.Protocol;
    try strict(ref, &ref_fields);
    const parent = try textOf(v, "parent");
    if (!w.eq(u8, try textOf(v, "frame"), "reverse_request") or !w.eq(u8, try textOf(v, "apiVersion"), w.version)) return error.Protocol;
    if (!w.identifier(try textOf(v, "id"), true) or (parent.len != 0 and !w.identifier(parent, true))) return error.Protocol;
    if (!w.identifier(try textOf(ref, "name"), false) or !w.identifier(try textOf(ref, "version"), true) or !w.identifier(try textOf(v, "operation"), false)) return error.Protocol;
    const stamp = v.object.get("deadline") orelse return error.Protocol;
    if (stamp != .string) return error.Protocol;
    _ = w.deadline(stamp.string) catch return error.Protocol;
}
const response_fields = [_][]const u8{ "frame", "apiVersion", "id", "payload", "error" };
const failure_fields = [_][]const u8{ "code", "message", "retryAfterMilliseconds" };
// Judges a reverse response, which a guest accepts only for the request id it is waiting on.
pub fn judgeReverseResponse(v: w.Value, id: []const u8) !Answer {
    try strict(v, &response_fields);
    if (!w.eq(u8, try textOf(v, "frame"), "reverse_response") or !w.eq(u8, try textOf(v, "apiVersion"), w.version) or !w.eq(u8, try textOf(v, "id"), id)) return error.Protocol;
    var failed = false;
    if (v.object.get("error")) |e| {
        if (e != .null) failed = true;
    }
    // A response carries a payload (null counts) or an error, never both or neither.
    if (failed == v.object.contains("payload")) return error.Protocol;
    if (!failed) return .{ .payload = v.object.get("payload").? };
    const failure = v.object.get("error").?;
    try strict(failure, &failure_fields);
    const retry = try countOf(failure, "retryAfterMilliseconds");
    const code = try textOf(failure, "code");
    const message = try textOf(failure, "message");
    if (!w.identifier(code, false) or message.len > 4096 or !std.unicode.utf8ValidateSlice(message) or retry > max_retry) return error.Protocol;
    return .{ .remote = .{ .code = code, .message = message, .retryAfterMilliseconds = @intCast(retry) } };
}

// ---- the connection ----

// One connection with the host as the guest sees it: whole frames in and out. violated is set when
// the host broke the rules, ended when the connection is over.
pub const Link = struct {
    context: *anyopaque,
    read: *const fn (*anyopaque, A) anyerror!?[]u8,
    write: *const fn (*anyopaque, []const u8) anyerror!void,
    issued: u64 = 0,
    ended: bool = false,
    violated: bool = false,

    fn fail(self: *Link) error{Protocol} {
        self.violated = true;
        self.ended = true;
        return error.Protocol;
    }
    pub fn send(self: *Link, bytes: []const u8) !void {
        self.write(self.context, bytes) catch {
            self.ended = true;
            return error.Closed;
        };
    }
    // Calls a host operation and waits for the answer. parent is the id of the call it belongs to and
    // deadline is a clock reading in nanoseconds. A host's error is left in remote and reported as
    // error.Remote. The host sends nothing but the answer during a call, so that is what is read next.
    pub fn reverse(self: *Link, a: A, parent: []const u8, ref: w.Ref, operation: []const u8, payload: w.Value, deadline: i128, remote: *?w.RemoteError) !w.Value {
        if (self.ended) return error.Closed;
        self.issued += 1;
        const id = try std.fmt.allocPrint(a, "g{d}", .{self.issued});
        var frame = w.object();
        try w.put(a, &frame, "frame", .{ .string = "reverse_request" });
        try w.put(a, &frame, "apiVersion", .{ .string = w.version });
        try w.put(a, &frame, "id", .{ .string = id });
        if (parent.len != 0) try w.put(a, &frame, "parent", .{ .string = parent });
        var contract = w.object();
        try w.put(a, &contract, "name", .{ .string = ref.name });
        try w.put(a, &contract, "version", .{ .string = ref.version });
        try w.put(a, &frame, "contract", contract);
        try w.put(a, &frame, "operation", .{ .string = operation });
        try w.put(a, &frame, "deadline", .{ .string = try w.timestamp(a, deadline) });
        try w.put(a, &frame, "payload", payload);
        judgeReverseRequest(frame) catch return error.Invalid;
        const bytes = w.encode(a, frame) catch return error.Invalid;
        try self.send(bytes);
        const maybe = self.read(self.context, a) catch |e| {
            if (e == error.Invalid or e == error.Truncated) return self.fail();
            self.ended = true;
            return error.Closed;
        };
        const line = maybe orelse {
            self.ended = true;
            return error.Closed;
        };
        const value = w.decode(a, line) catch return self.fail();
        const answer = judgeReverseResponse(value, id) catch return self.fail();
        switch (answer) {
            .payload => |p| return p,
            .remote => |r| {
                remote.* = r;
                return error.Remote;
            },
        }
    }
};

// Writes one progress frame. Fields that are empty or zero are left out, so a marker for events never
// reported is a log event with only a count of drops.
pub fn sendEvent(link: *Link, a: A, parent: []const u8, e: Event) !void {
    var frame = w.object();
    try w.put(a, &frame, "frame", .{ .string = "progress" });
    try w.put(a, &frame, "apiVersion", .{ .string = w.version });
    try w.put(a, &frame, "parent", .{ .string = parent });
    try w.put(a, &frame, "kind", .{ .string = e.kind });
    if (e.stream.len != 0) try w.put(a, &frame, "stream", .{ .string = e.stream });
    if (e.text.len != 0) try w.put(a, &frame, "text", .{ .string = e.text });
    if (e.completed != 0) try w.put(a, &frame, "completed", .{ .number_string = try std.fmt.allocPrint(a, "{d}", .{e.completed}) });
    if (e.total != 0) try w.put(a, &frame, "total", .{ .number_string = try std.fmt.allocPrint(a, "{d}", .{e.total}) });
    if (e.dropped != 0) try w.put(a, &frame, "dropped", .{ .number_string = try std.fmt.allocPrint(a, "{d}", .{e.dropped}) });
    try link.send(try w.encode(a, frame));
}
