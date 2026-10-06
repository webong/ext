const std = @import("std");
pub const Allocator = std.mem.Allocator;
pub const Value = std.json.Value;
pub const version = "ctx.plugin/v1";
pub const max_frame = 24 << 20;
pub const timeout_ns: i128 = 30_000_000_000;
pub const Identity = struct { id: []const u8, revision: []const u8, version: []const u8 = "" };
pub const Ref = struct { name: []const u8, version: []const u8 };
pub const Operation = struct { name: []const u8, surface: []const u8 = "" };
pub const Contract = struct { name: []const u8, version: []const u8, operations: []const Operation };
pub const Descriptor = struct { apiVersion: []const u8 = version, identity: Identity, contracts: []const Contract };
pub const Request = struct { apiVersion: []const u8, id: []const u8, plugin: Identity, contract: Ref, operation: []const u8, surface: []const u8 = "", deadline: []const u8, payload: Value = .null };
pub const RemoteError = struct { code: []const u8, message: []const u8, retryAfterMilliseconds: i64 = 0 };
pub const eq = std.mem.eql;

pub fn identifier(s: []const u8, revision: bool) bool {
    if (s.len == 0 or s.len > 256 or !std.ascii.isAlphanumeric(s[0])) return false;
    for (s) |b| if (!(std.ascii.isLower(b) or std.ascii.isDigit(b) or std.mem.indexOfScalar(u8, "._/-", b) != null or (revision and (std.ascii.isUpper(b) or b == '+' or b == ':')))) return false;
    return true;
}
pub fn validateIdentity(id: Identity) !void {
    if (!identifier(id.id, false) or !identifier(id.revision, true) or (id.version.len != 0 and !identifier(id.version, true))) return error.Invalid;
}
pub fn sameIdentity(a: Identity, b: Identity) bool {
    return eq(u8, a.id, b.id) and eq(u8, a.revision, b.revision) and eq(u8, a.version, b.version);
}
pub fn validate(d: Descriptor) !void {
    try validateIdentity(d.identity);
    if (!eq(u8, d.apiVersion, version) or d.contracts.len == 0 or d.contracts.len > 64) return error.Invalid;
    for (d.contracts, 0..) |c, i| {
        if (!identifier(c.name, false) or !identifier(c.version, true) or c.operations.len == 0 or c.operations.len > 256) return error.Invalid;
        for (d.contracts[0..i]) |p| if (eq(u8, p.name, c.name) and eq(u8, p.version, c.version)) return error.Invalid;
        for (c.operations, 0..) |op, j| {
            if (!identifier(op.name, false) or eq(u8, op.name, "plugin.hello") or (op.surface.len != 0 and !identifier(op.surface, false))) return error.Invalid;
            for (c.operations[0..j]) |p| if (eq(u8, p.name, op.name)) return error.Invalid;
        }
    }
}
pub fn lookup(d: Descriptor, ref: Ref, name: []const u8) !Operation {
    for (d.contracts) |c| if (eq(u8, c.name, ref.name) and eq(u8, c.version, ref.version)) {
        for (c.operations) |op| if (eq(u8, op.name, name)) return op;
    };
    return error.Unsupported;
}
pub fn match(a: Descriptor, b: Descriptor) !void {
    try validate(a);
    try validate(b);
    if (!sameIdentity(a.identity, b.identity) or a.contracts.len != b.contracts.len) return error.Mismatch;
    for (a.contracts) |c| {
        var found = false;
        for (b.contracts) |d| if (eq(u8, c.name, d.name) and eq(u8, c.version, d.version) and c.operations.len == d.operations.len) {
            found = true;
        };
        if (!found) return error.Mismatch;
        for (c.operations) |op| {
            const other = lookup(b, .{ .name = c.name, .version = c.version }, op.name) catch return error.Mismatch;
            if (!eq(u8, op.surface, other.surface)) return error.Mismatch;
        }
    }
}
pub fn validateRequest(d: Descriptor, r: Request) !void {
    try validate(d);
    if (!eq(u8, r.apiVersion, version) or !identifier(r.id, true)) return error.Invalid;
    _ = try deadline(r.deadline);
    if (!sameIdentity(d.identity, r.plugin) or !eq(u8, (try lookup(d, r.contract, r.operation)).surface, r.surface)) return error.Mismatch;
}

// Scan depth before allocating the JSON tree. The standard parser then rejects
// duplicate (including escaped) keys, invalid UTF-8 and trailing documents.
pub fn decode(a: Allocator, bytes: []const u8) !Value {
    if (bytes.len > max_frame or !std.unicode.utf8ValidateSlice(bytes)) return error.Invalid;
    var scanner = std.json.Scanner.initCompleteInput(a, bytes);
    defer scanner.deinit();
    var depth: usize = 0;
    while (true) {
        const token = try scanner.next();
        switch (token) {
            .object_begin, .array_begin => {
                depth += 1;
                if (depth > 65) return error.Invalid;
            },
            .object_end, .array_end => {
                if (depth == 0) return error.Invalid;
                depth -= 1;
            },
            .end_of_document => break,
            else => {
                if (depth > 64) return error.Invalid;
            },
        }
    }
    return std.json.parseFromSliceLeaky(Value, a, bytes, .{ .allocate = .alloc_always, .parse_numbers = false });
}
pub fn typed(comptime T: type, a: Allocator, value: Value) !T {
    return std.json.parseFromValueLeaky(T, a, value, .{});
}
pub fn encode(a: Allocator, value: anytype) ![]u8 {
    const bytes = try std.json.Stringify.valueAlloc(a, value, .{});
    if (bytes.len > max_frame) return error.Invalid;
    var check = std.heap.ArenaAllocator.init(a);
    defer check.deinit();
    _ = try decode(check.allocator(), bytes);
    return bytes;
}
pub fn object() Value {
    return .{ .object = .{} };
}
pub fn put(a: Allocator, obj: *Value, key: []const u8, value: Value) !void {
    try obj.object.put(a, key, value);
}
pub fn field(v: Value, key: []const u8) !Value {
    if (v != .object) return error.Invalid;
    return v.object.get(key) orelse error.Invalid;
}
pub fn string(v: Value) ![]const u8 {
    if (v != .string) return error.Invalid;
    return v.string;
}
pub fn response(a: Allocator, id: []const u8, payload: ?Value, remote: ?RemoteError) !Value {
    var out = object();
    try put(a, &out, "apiVersion", .{ .string = version });
    try put(a, &out, "id", .{ .string = id });
    if (remote) |e| {
        try put(a, &out, "error", try decode(a, try encode(a, e)));
    } else {
        try put(a, &out, "payload", payload orelse .null);
    }
    try validateResponse(a, out, id);
    return out;
}
pub fn validateResponse(a: Allocator, v: Value, id: []const u8) !void {
    if (v != .object) return error.Invalid;
    for (v.object.keys()) |k| if (!eq(u8, k, "apiVersion") and !eq(u8, k, "id") and !eq(u8, k, "payload") and !eq(u8, k, "error")) return error.Invalid;
    if (!eq(u8, try string(try field(v, "apiVersion")), version) or !eq(u8, try string(try field(v, "id")), id)) return error.Mismatch;
    const e = v.object.get("error");
    const has_error = if (e) |err| err != .null else false;
    if (v.object.contains("payload") == has_error) return error.Invalid;
    if (has_error) {
        const r = try typed(RemoteError, a, e.?);
        if (!identifier(r.code, false) or r.message.len > 4096 or r.retryAfterMilliseconds < 0) return error.Invalid;
    }
}

const Timespec = extern struct { sec: i64, nsec: c_long };
extern "c" fn clock_gettime(c_int, *Timespec) c_int;
extern "c" fn nanosleep(*const Timespec, ?*Timespec) c_int;
pub fn now() i128 {
    var ts: Timespec = undefined;
    if (clock_gettime(0, &ts) != 0) return std.math.maxInt(i128);
    return @as(i128, ts.sec) * 1_000_000_000 + ts.nsec;
}
pub fn sleepMillisecond() void {
    const ts: Timespec = .{ .sec = 0, .nsec = 1_000_000 };
    _ = nanosleep(&ts, null);
}
fn number(s: []const u8) !i64 {
    if (s.len == 0) return error.Invalid;
    for (s) |b| if (!std.ascii.isDigit(b)) return error.Invalid;
    return std.fmt.parseInt(i64, s, 10);
}
fn leap(y: i64) bool {
    return @mod(y, 4) == 0 and (@mod(y, 100) != 0 or @mod(y, 400) == 0);
}
fn daysMonth(y: i64, m: i64) i64 {
    return switch (m) {
        2 => if (leap(y)) 29 else 28,
        4, 6, 9, 11 => 30,
        else => 31,
    };
}
pub fn deadline(s: []const u8) !i128 {
    if (s.len < 20 or s[4] != '-' or s[7] != '-' or s[10] != 'T' or s[13] != ':' or s[16] != ':') return error.Invalid;
    const y = try number(s[0..4]);
    const m = try number(s[5..7]);
    const d = try number(s[8..10]);
    const h = try number(s[11..13]);
    const min = try number(s[14..16]);
    const sec = try number(s[17..19]);
    if (m < 1 or m > 12 or d < 1 or d > daysMonth(y, m) or h > 23 or min > 59 or sec > 59) return error.Invalid;
    var i: usize = 19;
    var nanos: i128 = 0;
    if (s[i] == '.') {
        i += 1;
        const start = i;
        while (i < s.len and std.ascii.isDigit(s[i])) : (i += 1) {
            if (i - start < 9) nanos = nanos * 10 + s[i] - '0';
        }
        if (i == start) return error.Invalid;
        var n = i - start;
        while (n < 9) : (n += 1) nanos *= 10;
    }
    if (i >= s.len) return error.Invalid;
    var offset: i64 = 0;
    if (s[i] == 'Z') {
        if (i + 1 != s.len) return error.Invalid;
    } else {
        if ((s[i] != '+' and s[i] != '-') or i + 6 != s.len or s[i + 3] != ':') return error.Invalid;
        const oh = try number(s[i + 1 .. i + 3]);
        const om = try number(s[i + 4 .. i + 6]);
        if (oh > 23 or om > 59) return error.Invalid;
        offset = (oh * 60 + om) * 60;
        if (s[i] == '-') offset = -offset;
    }
    var days: i64 = 0;
    var year: i64 = 1970;
    while (year < y) : (year += 1) days += if (leap(year)) @as(i64, 366) else 365;
    while (year > y) {
        year -= 1;
        days -= if (leap(year)) @as(i64, 366) else 365;
    }
    var month: i64 = 1;
    while (month < m) : (month += 1) days += daysMonth(y, month);
    if (y == 1 and m == 1 and d == 1 and h == 0 and min == 0 and sec == 0 and nanos == 0 and offset == 0) return error.Invalid;
    return (@as(i128, days + d - 1) * 86400 + h * 3600 + min * 60 + sec - offset) * 1_000_000_000 + nanos;
}
pub fn timestamp(a: Allocator, ns: i128) ![]u8 {
    if (ns < 0 or ns > 253402300799_999999999) return error.Invalid;
    const secs: i64 = @intCast(@divFloor(ns, 1_000_000_000));
    var days = @divFloor(secs, 86400);
    var year: i64 = 1970;
    while (days >= (if (leap(year)) @as(i64, 366) else 365)) {
        days -= if (leap(year)) @as(i64, 366) else 365;
        year += 1;
    }
    var month: i64 = 1;
    while (days >= daysMonth(year, month)) {
        days -= daysMonth(year, month);
        month += 1;
    }
    return std.fmt.allocPrint(a, "{d:0>4}-{d:0>2}-{d:0>2}T{d:0>2}:{d:0>2}:{d:0>2}.{d:0>9}Z", .{ @as(u64, @intCast(year)), @as(u64, @intCast(month)), @as(u64, @intCast(days + 1)), @as(u64, @intCast(@divFloor(@mod(secs, 86400), 3600))), @as(u64, @intCast(@divFloor(@mod(secs, 3600), 60))), @as(u64, @intCast(@mod(secs, 60))), @as(u64, @intCast(@mod(ns, 1_000_000_000))) });
}
