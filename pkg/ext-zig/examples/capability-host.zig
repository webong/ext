// Calls the capability contracts (ext.health, ext.configuration and ext.stream) of a guest on
// standard input and output, with the SDK's methods and stream.Client, and exits with an error on the
// first answer that is not the one the cross-language test expects of a Go guest
// (pkg/plugin-go/crosslang/capability_test.go: TestCapabilityHosts).
const std = @import("std");
const sdk = @import("ext_plugin");
const w = sdk.wire;
const cap = sdk.capability;
const stream = cap.stream;
const descriptor: w.Descriptor = .{
    .identity = .{ .id = "example/capabilities", .revision = "1" },
    .contracts = &.{
        .{ .name = "ext.health", .version = "v1", .operations = &.{.{ .name = "check", .surface = "observation" }} },
        .{ .name = "ext.configuration", .version = "v1", .operations = &.{.{ .name = "update", .surface = "configuration" }} },
        .{ .name = "ext.stream", .version = "v1", .operations = &.{ .{ .name = "open" }, .{ .name = "read" }, .{ .name = "close" } } },
    },
};
const Loader = struct {
    stdio: sdk.jsonline.Stdio = .{},
    conn: sdk.jsonline.Connection = undefined,
    line: sdk.host.JSONLine = undefined,
    fn connect(ctx: *anyopaque) !sdk.host.Backend {
        const self: *Loader = @ptrCast(@alignCast(ctx));
        self.conn = self.stdio.connection();
        self.line = .{ .connection = &self.conn };
        return self.line.backend();
    }
};
fn verify(d: w.Descriptor) !void {
    try w.validate(d);
}
fn allow(_: w.Request) !void {}
fn expect(ok: bool) !void {
    if (!ok) return error.Mismatch;
}
pub fn main() !void {
    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var loader: Loader = .{};
    var session = try sdk.host.Session.open(a, descriptor, verify, .{ .context = &loader, .connect = Loader.connect }, allow);
    defer session.deinit();

    const health = (try session.call(cap.HealthRequest, cap.Health, a, cap.health_method, .{})).value;
    try expect(w.eq(u8, health.status, "degraded") and !health.ready);

    const values = try w.decode(a, "{\"name\":\"a\"}");
    const configured = (try session.call(cap.Configuration, cap.Configured, a, cap.configuration_method, .{ .instance = "one", .revision = "r1", .values = values })).value;
    try expect(w.eq(u8, configured.instance, "one") and w.eq(u8, configured.revision, "r1"));
    // Go checks the values against the guest's schema and answers with a public code.
    switch (try session.call(cap.Configuration, cap.Configured, a, cap.configuration_method, .{ .instance = "one", .revision = "r2", .values = try w.decode(a, "{}") })) {
        .remote => |e| try expect(w.eq(u8, e.code, "invalid_config")),
        .value => return error.ExpectedRemote,
    }

    var client = (try stream.Client.open(&session, a, try w.decode(a, "{}"))).value;
    try expect(client.id().len > 0 and client.id().len <= 64);
    const first = (try client.read(a, 2)).value;
    try expect(first.items.len == 2 and !first.done);
    try expect(w.eq(u8, first.items[0].number_string, "1") and w.eq(u8, first.items[1].number_string, "2"));
    const second = (try client.read(a, 2)).value;
    try expect(second.items.len == 1 and second.done);
    try expect(w.eq(u8, second.items[0].number_string, "3"));
    const after = (try client.read(a, 2)).value;
    try expect(after.items.len == 0 and after.done);
    switch (try client.close(a)) {
        .value => {},
        .remote => return error.UnexpectedRemote,
    }

    // A stream closed before its end: the guest releases it, and a later close is not an error.
    var early = (try stream.Client.open(&session, a, try w.decode(a, "{}"))).value;
    const part = (try early.read(a, 1)).value;
    try expect(part.items.len == 1 and !part.done);
    switch (try early.close(a)) {
        .value => {},
        .remote => return error.UnexpectedRemote,
    }
    switch (try early.close(a)) {
        .value => {},
        .remote => return error.UnexpectedRemote,
    }
    session.close();
}
