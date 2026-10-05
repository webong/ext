const std = @import("std");
const w = @import("wire.zig");
const author = @import("author.zig");
// Callbacks own I/O deadlines and cancellation. close must unblock read/write.
pub const IO = struct {
    context: *anyopaque,
    read: *const fn (*anyopaque, []u8, i128) anyerror!usize,
    write: *const fn (*anyopaque, []const u8, i128) anyerror!usize,
    close: *const fn (*anyopaque) void,
};
pub const Connection = struct {
    io: IO,
    buffer: [4096]u8 = undefined,
    start: usize = 0,
    end: usize = 0,
    closed: bool = false,
    pub fn close(self: *Connection) void {
        if (!self.closed) {
            self.closed = true;
            self.io.close(self.io.context);
        }
    }
    pub fn readFrame(self: *Connection, a: w.Allocator, deadline: i128) !?[]u8 {
        if (self.closed) return error.Closed;
        var line: std.ArrayList(u8) = .empty;
        while (true) {
            if (self.start == self.end) {
                self.start = 0;
                self.end = try self.io.read(self.io.context, &self.buffer, deadline);
                if (self.end > self.buffer.len) return error.Invalid;
                if (self.end == 0) {
                    if (line.items.len == 0) return null;
                    return error.Truncated;
                }
            }
            const bytes = self.buffer[self.start..self.end];
            const newline = std.mem.indexOfScalar(u8, bytes, '\n');
            const n = if (newline) |index| index + 1 else bytes.len;
            if (line.items.len + n > w.max_frame + 1) return error.Invalid;
            try line.appendSlice(a, bytes[0..n]);
            self.start += n;
            if (newline != null) {
                _ = line.pop();
                return line.items;
            }
        }
    }
    pub fn writeFrame(self: *Connection, bytes: []const u8, deadline: i128) !void {
        if (self.closed) return error.Closed;
        if (bytes.len > w.max_frame) return error.Invalid;
        try self.writeAll(bytes, deadline);
        try self.writeAll("\n", deadline);
    }
    fn writeAll(self: *Connection, bytes: []const u8, deadline: i128) !void {
        var pos: usize = 0;
        while (pos < bytes.len) {
            const n = try self.io.write(self.io.context, bytes[pos..], deadline);
            if (n == 0 or n > bytes.len - pos) return error.Transport;
            pos += n;
        }
    }
};
pub fn serve(guest: *const author.Guest, conn: *Connection, a: w.Allocator) !void {
    defer conn.close();
    var hello = false;
    while (true) {
        var arena = std.heap.ArenaAllocator.init(a);
        defer arena.deinit();
        const scratch = arena.allocator();
        const line = (try conn.readFrame(scratch, std.math.maxInt(i128))) orelse return;
        const value = try w.decode(scratch, line);
        const response = if (!hello) blk: {
            try validateHello(scratch, value);
            hello = true;
            break :blk try w.response(scratch, "hello", try w.decode(scratch, try w.encode(scratch, guest.descriptor)), null);
        } else try guest.invoke(scratch, try w.typed(w.Request, scratch, value));
        try conn.writeFrame(try w.encode(scratch, response), w.now() + w.timeout_ns);
    }
}
fn validateHello(a: w.Allocator, value: w.Value) !void {
    const Hello = struct { apiVersion: []const u8, id: []const u8, operation: []const u8, deadline: []const u8, plugin: struct { id: []const u8 = "", revision: []const u8 = "", version: []const u8 = "" } = .{}, contract: struct { name: []const u8 = "", version: []const u8 = "" } = .{}, surface: []const u8 = "" };
    const h = try w.typed(Hello, a, value);
    if (!w.eq(u8, h.apiVersion, w.version) or !w.eq(u8, h.id, "hello") or !w.eq(u8, h.operation, "plugin.hello") or h.plugin.id.len != 0 or h.plugin.revision.len != 0 or h.plugin.version.len != 0 or h.contract.name.len != 0 or h.contract.version.len != 0 or h.surface.len != 0) return error.Invalid;
    if (w.now() >= try w.deadline(h.deadline)) return error.Deadline;
}
extern "c" fn read(c_int, [*]u8, usize) isize;
extern "c" fn write(c_int, [*]const u8, usize) isize;
extern "c" fn close(c_int) c_int;
// Standalone commands own stdin/stdout. No logging is allowed on stdout.
// These libc streams block; embedding hosts should supply deadline-aware IO.
pub const Stdio = struct {
    pub fn connection(self: *Stdio) Connection {
        return .{ .io = .{ .context = self, .read = readInput, .write = writeOutput, .close = closeStreams } };
    }
    fn readInput(_: *anyopaque, bytes: []u8, deadline: i128) !usize {
        if (w.now() >= deadline) return error.Deadline;
        const n = read(0, bytes.ptr, bytes.len);
        if (n < 0) return error.Transport;
        return @intCast(n);
    }
    fn writeOutput(_: *anyopaque, bytes: []const u8, deadline: i128) !usize {
        if (w.now() >= deadline) return error.Deadline;
        const n = write(1, bytes.ptr, bytes.len);
        if (n < 0) return error.Transport;
        return @intCast(n);
    }
    fn closeStreams(_: *anyopaque) void {
        _ = close(0);
        _ = close(1);
    }
};
