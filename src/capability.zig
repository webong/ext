// Typed definitions of the SDK's optional capability contracts, with the same wire shapes and limits
// as the Go library: ext.health, ext.configuration and ext.stream. A guest registers handlers for
// these methods with Registry.register; a host calls them through the same Method values, and
// stream.Client pulls a stream over a host Session.
const std = @import("std");
const w = @import("wire.zig");
const author = @import("author.zig");
const host = @import("host.zig");
const Value = w.Value;
const max_name = 256;
const max_id = 64;
fn contract(comptime name: []const u8) w.Ref {
    return .{ .name = name, .version = "v1" };
}

// ext.health check. The status is one of healthy, degraded and unhealthy.
pub const HealthRequest = struct {};
pub const Health = struct { status: []const u8, ready: bool };
pub const health_method: author.Method(HealthRequest, Health) = .{
    .contract = contract("ext.health"),
    .operation = .{ .name = "check", .surface = "observation" },
    .validate_output = validateHealth,
};
fn validateHealth(h: Health) !void {
    for ([_][]const u8{ "healthy", "degraded", "unhealthy" }) |status| {
        if (w.eq(u8, h.status, status)) return;
    }
    return error.Invalid;
}

// ext.configuration update. values is any JSON document that the guest's own schema then checks; it
// must not carry credentials. instance and revision are 1 to 256 bytes.
pub const Configuration = struct { instance: []const u8, revision: []const u8, values: Value };
pub const Configured = struct { instance: []const u8, revision: []const u8 };
pub const configuration_method: author.Method(Configuration, Configured) = .{
    .contract = contract("ext.configuration"),
    .operation = .{ .name = "update", .surface = "configuration" },
    .validate_input = validateConfiguration,
};
fn validateConfiguration(c: Configuration) !void {
    if (c.instance.len == 0 or c.instance.len > max_name or c.revision.len == 0 or c.revision.len > max_name) return error.Invalid;
}

// ext.stream: a pull stream of JSON items, read in numbered batches. The guest owns the stream's
// state (scope, lifetime, the sequence of reads); this file defines what travels and Client.
pub const stream = struct {
    pub const max_items = 256;
    pub const max_batch_bytes = 1 << 20;

    pub const OpenRequest = struct { parameters: Value };
    pub const OpenResponse = struct { id: []const u8 };
    // sequence counts reads from 1 and limit is 1 to max_items. Both are whole JSON numbers, as in
    // Go: 1.0 and 1e0 are not integers here, though the standard parser would take them.
    pub const ReadRequest = struct {
        id: []const u8,
        sequence: u64,
        limit: i64,
        pub fn jsonParseFromValue(_: std.mem.Allocator, source: Value, _: std.json.ParseOptions) std.json.ParseFromValueError!ReadRequest {
            if (source != .object) return error.UnexpectedToken;
            for (source.object.keys()) |key| {
                if (!w.eq(u8, key, "id") and !w.eq(u8, key, "sequence") and !w.eq(u8, key, "limit")) return error.UnknownField;
            }
            const id = source.object.get("id") orelse return error.MissingField;
            if (id != .string) return error.UnexpectedToken;
            return .{
                .id = id.string,
                .sequence = try whole(u64, source.object.get("sequence") orelse return error.MissingField),
                .limit = try whole(i64, source.object.get("limit") orelse return error.MissingField),
            };
        }
    };
    pub const Batch = struct { items: []const Value, done: bool };
    pub const CloseRequest = struct { id: []const u8 };
    pub const Empty = struct {};

    pub const open_method: author.Method(OpenRequest, OpenResponse) = .{
        .contract = contract("ext.stream"),
        .operation = .{ .name = "open" },
        .validate_output = validateOpened,
    };
    pub const read_method: author.Method(ReadRequest, Batch) = .{
        .contract = contract("ext.stream"),
        .operation = .{ .name = "read" },
        .validate_input = validateRead,
        .validate_output = validateBatch,
    };
    pub const close_method: author.Method(CloseRequest, Empty) = .{
        .contract = contract("ext.stream"),
        .operation = .{ .name = "close" },
        .validate_input = validateClose,
    };

    fn validId(id: []const u8) bool {
        return id.len != 0 and id.len <= max_id;
    }
    fn validateOpened(r: OpenResponse) !void {
        if (!validId(r.id)) return error.Invalid;
    }
    fn validateRead(r: ReadRequest) !void {
        if (!validId(r.id) or r.sequence == 0 or r.limit < 1 or r.limit > max_items) return error.Invalid;
    }
    fn validateClose(r: CloseRequest) !void {
        if (!validId(r.id)) return error.Invalid;
    }
    // A batch has at most max_items items and max_batch_bytes of compact JSON.
    pub fn validateBatch(b: Batch) !void {
        if (b.items.len > max_items) return error.Invalid;
        var size: u64 = 0;
        for (b.items) |item| {
            var count: std.Io.Writer.Discarding = .init(&.{});
            std.json.Stringify.value(item, .{}, &count.writer) catch return error.Invalid;
            size += count.fullCount();
            if (size > max_batch_bytes) return error.Invalid;
        }
    }
    // A whole JSON number: parseInt takes no fraction or exponent (the standard parser would, for 1.0),
    // and it fits.
    fn whole(comptime T: type, v: Value) std.json.ParseFromValueError!T {
        switch (v) {
            .integer => |i| return std.math.cast(T, i) orelse error.Overflow,
            .number_string => |s| {
                return std.fmt.parseInt(T, s, 10);
            },
            else => return error.UnexpectedToken,
        }
    }

    // One stream pulled over a host Session. Calls are serial, as the Session's are, and a failed
    // read is not retried: items may already have been consumed, so the stream is over.
    pub const Client = struct {
        session: *host.Session,
        id_bytes: [max_id]u8 = undefined,
        id_len: usize = 0,
        sequence: u64 = 0,
        done: bool = false,
        pub fn open(session: *host.Session, a: w.Allocator, parameters: Value) !host.Result(Client) {
            switch (try session.call(OpenRequest, OpenResponse, a, open_method, .{ .parameters = parameters })) {
                .remote => |r| return .{ .remote = r },
                .value => |opened| {
                    var client: Client = .{ .session = session, .id_len = opened.id.len };
                    @memcpy(client.id_bytes[0..opened.id.len], opened.id);
                    return .{ .value = client };
                },
            }
        }
        pub fn id(self: *const Client) []const u8 {
            return self.id_bytes[0..self.id_len];
        }
        // Reads up to limit items. Once the stream is done, every read is an empty final batch. The
        // batch lives in a.
        pub fn read(self: *Client, a: w.Allocator, limit: i64) !host.Result(Batch) {
            if (self.done) return .{ .value = .{ .items = &.{}, .done = true } };
            if (limit < 1 or limit > max_items) return error.Invalid;
            self.sequence += 1;
            self.done = true;
            const result = try self.session.call(ReadRequest, Batch, a, read_method, .{ .id = self.id(), .sequence = self.sequence, .limit = limit });
            switch (result) {
                .remote => return result,
                .value => |batch| {
                    if (batch.items.len > limit) return error.Invalid;
                    self.done = batch.done;
                    return result;
                },
            }
        }
        // Ends the stream; the guest releases it.
        pub fn close(self: *Client, a: w.Allocator) !host.Result(Empty) {
            self.done = true;
            return self.session.call(CloseRequest, Empty, a, close_method, .{ .id = self.id() });
        }
    };
};
