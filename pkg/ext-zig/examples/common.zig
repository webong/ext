const sdk = @import("ext_plugin");
const w = sdk.wire;
pub fn allow(_: w.Request) !void {}
pub fn factory(a: w.Allocator) !sdk.author.Guest {
    var r = try sdk.author.Registry.init(a, .{ .id = "ctx/conformance", .revision = "fixture-1" });
    defer r.deinit();
    inline for (.{ "echo", "wait", "private-error", "public-error" }) |name| {
        try r.register(w.Value, w.Value, .{ .contract = .{ .name = "ext.conformance", .version = "v1" }, .operation = .{ .name = name } }, handle);
    }
    return r.guest(allow);
}
fn handle(ctx: *sdk.author.Context, r: w.Request, input: w.Value) !w.Value {
    if (w.eq(u8, r.operation, "wait")) {
        while (true) {
            try ctx.check();
            w.sleepMillisecond();
        }
    }
    if (w.eq(u8, r.operation, "private-error")) return error.PrivateDiagnostic;
    if (w.eq(u8, r.operation, "public-error")) return ctx.fail(.{ .code = "busy", .message = "try later", .retryAfterMilliseconds = 10 });
    return input;
}
