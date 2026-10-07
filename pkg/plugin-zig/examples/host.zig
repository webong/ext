const std = @import("std");
const sdk = @import("ext_plugin");
const common = @import("common.zig");
const w = sdk.wire;
const Loader = struct {
    path: []const u8,
    library: ?sdk.host.CShared = null,
    stdio: sdk.jsonline.Stdio = .{},
    conn: sdk.jsonline.Connection = undefined,
    line: sdk.host.JSONLine = undefined,
    fn connect(ctx: *anyopaque) !sdk.host.Backend {
        const self: *Loader = @ptrCast(@alignCast(ctx));
        if (w.eq(u8, self.path, "--stdio")) {
            self.conn = self.stdio.connection();
            self.line = .{ .connection = &self.conn };
            return self.line.backend();
        }
        self.library = try sdk.host.CShared.open(self.path);
        return self.library.?.backend();
    }
};
// Explicit local fixture path supplied by the runner. Real hosts authenticate
// the reviewed artifact in verify before connect executes any foreign code.
fn verify(d: w.Descriptor) !void {
    try w.validate(d);
}
pub fn main(init: std.process.Init) !void {
    const a = init.arena.allocator();
    const args = try init.minimal.args.toSlice(a);
    if (args.len != 2) return error.ExpectedLibraryPathOrStdio;
    var guest = try common.factory(a);
    defer guest.deinit();
    var loader: Loader = .{ .path = args[1] };
    var session = try sdk.host.Session.open(a, guest.descriptor, verify, .{ .context = &loader, .connect = Loader.connect }, common.allow);
    defer session.deinit();
    const ref: w.Ref = .{ .name = "ext.conformance", .version = "v1" };
    const out = try session.callRaw(a, ref, "echo", try w.decode(a, "{\"value\":7}"), 3_000_000_000);
    switch (out) {
        .remote => return error.UnexpectedRemote,
        .value => |v| {
            if (!w.eq(u8, try w.encode(a, v), "{\"value\":7}")) return error.Mismatch;
        },
    }
    const remote = try session.callRaw(a, ref, "public-error", .null, 3_000_000_000);
    switch (remote) {
        .value => return error.ExpectedRemote,
        .remote => |e| {
            if (!w.eq(u8, e.code, "busy") or e.retryAfterMilliseconds != 10) return error.Mismatch;
        },
    }
    session.close();
}
