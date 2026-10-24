// The capability contracts (ext.health, ext.configuration and ext.stream) served as a command, for
// the cross-language test that calls them with the Go library's own clients
// (pkg/plugin-go/crosslang/capability_test.go). It serves standard input and output, natively or as
// a WASI command.
const std = @import("std");
const sdk = @import("ext_plugin");
const w = sdk.wire;
const cap = sdk.capability;
const stream = cap.stream;
fn allow(_: w.Request) !void {}
const items = [_]w.Value{ .{ .integer = 1 }, .{ .integer = 2 } };
// The sequence the next read must carry. A request the SDK refuses never reaches the handler, so it
// never moves this.
var expected: u64 = 1;
pub fn factory(a: w.Allocator) !sdk.author.Guest {
    var r = try sdk.author.Registry.init(a, .{ .id = "example/capabilities", .revision = "1" });
    defer r.deinit();
    try r.register(cap.HealthRequest, cap.Health, cap.health_method, health);
    try r.register(cap.Configuration, cap.Configured, cap.configuration_method, configure);
    try r.register(stream.OpenRequest, stream.OpenResponse, stream.open_method, open);
    try r.register(stream.ReadRequest, stream.Batch, stream.read_method, read);
    try r.register(stream.CloseRequest, stream.Empty, stream.close_method, close);
    return r.guest(allow);
}
pub fn main() !void {
    var guest = try factory(std.heap.page_allocator);
    defer guest.deinit();
    var stdio: sdk.jsonline.Stdio = .{};
    var conn = stdio.connection();
    try sdk.jsonline.serve(&guest, &conn, std.heap.page_allocator);
}
fn health(_: *sdk.author.Context, _: w.Request, _: cap.HealthRequest) !cap.Health {
    return .{ .status = "degraded", .ready = false };
}
// A revision named reject is refused with a public error, as a guest whose schema rejects the values
// would.
fn configure(ctx: *sdk.author.Context, _: w.Request, c: cap.Configuration) !cap.Configured {
    if (w.eq(u8, c.revision, "reject")) return ctx.fail(.{ .code = "invalid_config", .message = "configuration does not satisfy schema" });
    return .{ .instance = c.instance, .revision = c.revision };
}
fn open(_: *sdk.author.Context, _: w.Request, _: stream.OpenRequest) !stream.OpenResponse {
    expected = 1;
    return .{ .id = "s1" };
}
// Two items at most, and the stream ends with the second batch.
fn read(ctx: *sdk.author.Context, _: w.Request, r: stream.ReadRequest) !stream.Batch {
    if (r.sequence != expected) return ctx.fail(.{ .code = "sequence", .message = "stream sequence mismatch" });
    expected += 1;
    return .{ .items = items[0..@min(@as(usize, @intCast(r.limit)), items.len)], .done = r.sequence >= 2 };
}
fn close(_: *sdk.author.Context, _: w.Request, _: stream.CloseRequest) !stream.Empty {
    return .{};
}
