const std = @import("std");
// Explicit C ABI declarations for Zig 0.17.0. Header layout is exercised by the runner.
const c = struct {
    const ctx_host = opaque {};
    const FILE = opaque {};
    const ctx_status = i32;
    const CTX_OK = 0;
    const CTX_HOST_MAX_FRAME = 24 * 1024 * 1024;
    const SEEK_SET = 0;
    const SEEK_END = 2;
    const Policy = *const fn (?*anyopaque, [*c]const u8, usize) callconv(.c) i32;
    const ctx_host_options = extern struct { abi_version: u32, struct_size: u32, descriptor: [*c]const u8, descriptor_len: usize, verify: Policy, authorize: Policy, user: ?*anyopaque };
    const ProcessOptions = extern struct { executable: [*c]const u8 };
    const BackendOptions = extern struct { struct_size: u32, kind: u32, config: *const anyopaque, config_size: usize };
    const ctx_buffer = extern struct { data: [*c]u8, len: usize };
    extern fn ctx_host_create(*const ctx_host_options, *const BackendOptions, *?*ctx_host) ctx_status;
    extern fn ctx_host_start(?*ctx_host, u32) ctx_status;
    extern fn ctx_host_invoke(?*ctx_host, [*c]const u8, usize, u32, *ctx_buffer) ctx_status;
    extern fn ctx_host_destroy(?*ctx_host) void;
    extern fn ctx_buffer_free(*ctx_buffer) void;
    extern fn fopen([*c]const u8, [*c]const u8) ?*FILE;
    extern fn fclose(*FILE) c_int;
    extern fn fseek(*FILE, c_long, c_int) c_int;
    extern fn ftell(*FILE) c_long;
    extern fn fread([*c]u8, usize, usize, *FILE) usize;
    extern fn puts([*c]const u8) c_int;
};
// Fixture policy only; real applications supply artifact and operation policy.
fn allow(_: ?*anyopaque, _: [*c]const u8, _: usize) callconv(.c) i32 {
    return 0;
}
fn check(s: c.ctx_status) !void {
    if (s != c.CTX_OK) return error.EngineFailure;
}
fn read(a: std.mem.Allocator, path: []const u8) ![]u8 {
    const z = try a.dupeSentinel(u8, path, 0);
    const f = c.fopen(z.ptr, "rb") orelse return error.ReadFailed;
    defer _ = c.fclose(f);
    if (c.fseek(f, 0, c.SEEK_END) != 0) return error.ReadFailed;
    const n = c.ftell(f);
    if (n < 0 or n > c.CTX_HOST_MAX_FRAME) return error.ReadFailed;
    if (c.fseek(f, 0, c.SEEK_SET) != 0) return error.ReadFailed;
    const b = try a.alloc(u8, @intCast(n));
    if (c.fread(b.ptr, 1, b.len, f) != b.len) return error.ReadFailed;
    return b;
}
pub fn main(init: std.process.Init) !void {
    const a = init.arena.allocator();
    const args = try init.minimal.args.toSlice(a);
    if (args.len < 4) return error.ExpectedGuestDescriptorRequests;
    const path = try a.dupeSentinel(u8, args[1], 0);
    const descriptor = try read(a, args[2]);
    const process: c.ProcessOptions = .{ .executable = path.ptr };
    const backend: c.BackendOptions = .{ .struct_size = @sizeOf(c.BackendOptions), .kind = 1, .config = &process, .config_size = @sizeOf(c.ProcessOptions) };
    var options: c.ctx_host_options = .{ .abi_version = 2, .struct_size = @sizeOf(c.ctx_host_options), .descriptor = descriptor.ptr, .descriptor_len = descriptor.len, .verify = allow, .authorize = allow, .user = null };
    var host: ?*c.ctx_host = null;
    try check(c.ctx_host_create(&options, &backend, &host));
    defer c.ctx_host_destroy(host);
    try check(c.ctx_host_start(host, 3000));
    for (args[3..]) |file| {
        const request = try read(a, file);
        var out: c.ctx_buffer = .{ .data = null, .len = 0 };
        defer c.ctx_buffer_free(&out);
        try check(c.ctx_host_invoke(host, request.ptr, request.len, 3000, &out));
        const z = try a.dupeSentinel(u8, out.data[0..out.len], 0);
        _ = c.puts(z.ptr);
    }
}
