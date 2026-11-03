const std = @import("std");
pub const Allocator = std.mem.Allocator;
pub const Value = std.json.Value;
pub const version = "ext.plugin/v1";
pub const max_frame = 24 << 20;
pub const timeout_ns: i128 = 30_000_000_000;
pub const Identity = struct { id: []const u8, revision: []const u8, version: []const u8 = "" };
pub const Ref = struct { name: []const u8, version: []const u8 };
// progress says the operation reports progress events while it runs; it is not valid on a host
// operation in Descriptor.reverse, the contracts the plugin may call on its host.
pub const Operation = struct { name: []const u8, surface: []const u8 = "", progress: bool = false };
pub const Contract = struct { name: []const u8, version: []const u8, operations: []const Operation };
pub const Descriptor = struct { apiVersion: []const u8 = version, identity: Identity, contracts: []const Contract, reverse: []const Contract = &.{} };
// The rate at which the host lets a call report progress; a member that is not positive uses the default.
pub const Rate = struct { eventsPerSecond: i64 = 0, bytesPerSecond: i64 = 0 };
// parent is the call that issued the request, empty for a session-scoped one.
pub const Request = struct { apiVersion: []const u8, id: []const u8, plugin: Identity, contract: Ref, operation: []const u8, surface: []const u8 = "", deadline: []const u8, payload: Value = .null, progress: ?Rate = null, parent: []const u8 = "" };
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
    if (!eq(u8, d.apiVersion, version) or d.contracts.len == 0 or d.contracts.len > 64 or d.reverse.len > 64) return error.Invalid;
    try validateContracts(d.contracts, false);
    try validateContracts(d.reverse, true);
}
fn validateContracts(contracts: []const Contract, reverse: bool) !void {
    for (contracts, 0..) |c, i| {
        if (!identifier(c.name, false) or !identifier(c.version, true) or c.operations.len == 0 or c.operations.len > 256) return error.Invalid;
        for (contracts[0..i]) |p| if (eq(u8, p.name, c.name) and eq(u8, p.version, c.version)) return error.Invalid;
        for (c.operations, 0..) |op, j| {
            if (!identifier(op.name, false) or eq(u8, op.name, "plugin.hello") or (op.surface.len != 0 and !identifier(op.surface, false)) or (reverse and op.progress)) return error.Invalid;
            for (c.operations[0..j]) |p| if (eq(u8, p.name, op.name)) return error.Invalid;
        }
    }
}
fn find(contracts: []const Contract, ref: Ref, name: []const u8) !Operation {
    for (contracts) |c| if (eq(u8, c.name, ref.name) and eq(u8, c.version, ref.version)) {
        for (c.operations) |op| if (eq(u8, op.name, name)) return op;
    };
    return error.Unsupported;
}
pub fn lookup(d: Descriptor, ref: Ref, name: []const u8) !Operation {
    return find(d.contracts, ref, name);
}
// An operation the plugin declared it may call on its host.
pub fn lookupReverse(d: Descriptor, ref: Ref, name: []const u8) !Operation {
    return find(d.reverse, ref, name);
}
pub fn match(a: Descriptor, b: Descriptor) !void {
    try validate(a);
    try validate(b);
    if (!sameIdentity(a.identity, b.identity)) return error.Mismatch;
    try matchContracts(a.contracts, b.contracts);
    try matchContracts(a.reverse, b.reverse);
}
fn matchContracts(a: []const Contract, b: []const Contract) !void {
    if (a.len != b.len) return error.Mismatch;
    for (a) |c| {
        var found = false;
        for (b) |d| if (eq(u8, c.name, d.name) and eq(u8, c.version, d.version) and c.operations.len == d.operations.len) {
            found = true;
        };
        if (!found) return error.Mismatch;
        for (c.operations) |op| {
            const other = find(b, .{ .name = c.name, .version = c.version }, op.name) catch return error.Mismatch;
            if (!eq(u8, op.surface, other.surface) or op.progress != other.progress) return error.Mismatch;
        }
    }
}
pub fn validateRequest(d: Descriptor, r: Request) !void {
    try validate(d);
    if (!eq(u8, r.apiVersion, version) or !identifier(r.id, true)) return error.Invalid;
    _ = try deadline(r.deadline);
    if (!sameIdentity(d.identity, r.plugin) or !eq(u8, (try lookup(d, r.contract, r.operation)).surface, r.surface)) return error.Mismatch;
}

// The strict readers below take a decoded JSON tree the way the Go contract decodes it: a member it
// does not know is refused, a member it leaves alone when it is null is empty, and a number that is
// not a whole one that fits is refused. Slices point into the tree.
fn keysWithin(v: Value, allowed: []const []const u8) !void {
    if (v != .object) return error.Invalid;
    for (v.object.keys()) |k| {
        var known = false;
        for (allowed) |name| {
            if (eq(u8, k, name)) known = true;
        }
        if (!known) return error.Invalid;
    }
}
// A member that is a string when present; null is not.
fn memberString(v: Value, key: []const u8) ![]const u8 {
    const m = v.object.get(key) orelse return "";
    return string(m);
}
// A member that is a string when present, and empty when absent or null.
fn nullableString(v: Value, key: []const u8) ![]const u8 {
    const m = v.object.get(key) orelse return "";
    if (m == .null) return "";
    return string(m);
}
const identity_fields = [_][]const u8{ "id", "revision", "version" };
const ref_fields = [_][]const u8{ "name", "version" };
const operation_fields = [_][]const u8{ "name", "surface", "progress" };
const contract_fields = [_][]const u8{ "name", "version", "operations" };
const descriptor_fields = [_][]const u8{ "apiVersion", "identity", "contracts", "reverse" };
const rate_fields = [_][]const u8{ "eventsPerSecond", "bytesPerSecond" };
const request_fields = [_][]const u8{ "apiVersion", "id", "plugin", "contract", "operation", "surface", "deadline", "payload", "progress", "parent" };
fn operationFrom(v: Value) !Operation {
    try keysWithin(v, &operation_fields);
    var progress = false;
    if (v.object.get("progress")) |p| {
        switch (p) {
            .null => {},
            .bool => |b| progress = b,
            else => return error.Invalid,
        }
    }
    return .{ .name = try string(try field(v, "name")), .surface = try nullableString(v, "surface"), .progress = progress };
}
fn contractFrom(a: Allocator, v: Value) !Contract {
    try keysWithin(v, &contract_fields);
    const list = try field(v, "operations");
    if (list != .array) return error.Invalid;
    const operations = try a.alloc(Operation, list.array.items.len);
    for (list.array.items, 0..) |item, i| operations[i] = try operationFrom(item);
    return .{ .name = try string(try field(v, "name")), .version = try string(try field(v, "version")), .operations = operations };
}
fn contractsFrom(a: Allocator, v: Value) ![]const Contract {
    if (v != .array) return error.Invalid;
    const contracts = try a.alloc(Contract, v.array.items.len);
    for (v.array.items, 0..) |item, i| contracts[i] = try contractFrom(a, item);
    return contracts;
}
// A descriptor as the Go contract decodes it; validate checks the rules that remain.
pub fn descriptorFromValue(a: Allocator, v: Value) !Descriptor {
    try keysWithin(v, &descriptor_fields);
    const identity_value = try field(v, "identity");
    try keysWithin(identity_value, &identity_fields);
    var reverse: []const Contract = &.{};
    if (v.object.get("reverse")) |r| {
        if (r != .null) reverse = try contractsFrom(a, r);
    }
    return .{
        .apiVersion = try string(try field(v, "apiVersion")),
        .identity = .{ .id = try string(try field(identity_value, "id")), .revision = try string(try field(identity_value, "revision")), .version = try memberString(identity_value, "version") },
        .contracts = try contractsFrom(a, try field(v, "contracts")),
        .reverse = reverse,
    };
}
// An identity or a contract reference in a request: absent and null are empty, and a member that is
// present is a string.
fn requestIdentity(v: ?Value) !Identity {
    const value = v orelse return .{ .id = "", .revision = "", .version = "" };
    if (value == .null) return .{ .id = "", .revision = "", .version = "" };
    try keysWithin(value, &identity_fields);
    return .{ .id = try memberString(value, "id"), .revision = try memberString(value, "revision"), .version = try memberString(value, "version") };
}
fn requestRef(v: ?Value) !Ref {
    const value = v orelse return .{ .name = "", .version = "" };
    if (value == .null) return .{ .name = "", .version = "" };
    try keysWithin(value, &ref_fields);
    return .{ .name = try memberString(value, "name"), .version = try memberString(value, "version") };
}
fn rateMember(v: Value, key: []const u8) !i64 {
    const m = v.object.get(key) orelse return 0;
    if (m == .null) return 0;
    if (m != .number_string) return error.Invalid;
    return std.fmt.parseInt(i64, m.number_string, 10) catch return error.Invalid;
}
fn rateFrom(v: ?Value) !?Rate {
    const value = v orelse return null;
    if (value == .null) return null;
    try keysWithin(value, &rate_fields);
    return Rate{ .eventsPerSecond = try rateMember(value, "eventsPerSecond"), .bytesPerSecond = try rateMember(value, "bytesPerSecond") };
}
// A request as the Go contract decodes it; validateRequest checks it against a descriptor.
pub fn requestFromValue(v: Value) !Request {
    try keysWithin(v, &request_fields);
    return .{
        .apiVersion = try memberString(v, "apiVersion"),
        .id = try string(try field(v, "id")),
        .plugin = try requestIdentity(v.object.get("plugin")),
        .contract = try requestRef(v.object.get("contract")),
        .operation = try memberString(v, "operation"),
        .surface = try memberString(v, "surface"),
        .deadline = try memberString(v, "deadline"),
        .payload = v.object.get("payload") orelse .null,
        .progress = try rateFrom(v.object.get("progress")),
        .parent = try nullableString(v, "parent"),
    };
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
