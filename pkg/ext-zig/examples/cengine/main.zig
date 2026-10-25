const std = @import("std");
// Explicit C ABI declarations for Zig 0.17.0. Header layout is exercised by the runner.
const c = struct {
    const ext_host = opaque {};
    const FILE = opaque {};
    const ext_status = i32;
    const EXT_OK = 0;
    const EXT_HOST_MAX_FRAME = 24 * 1024 * 1024;
    const SEEK_SET = 0;
    const SEEK_END = 2;
    const Policy = *const fn (?*anyopaque, [*c]const u8, usize) callconv(.c) i32;
    const ext_host_options = extern struct { abi_version: u32, struct_size: u32, descriptor: [*c]const u8, descriptor_len: usize, verify: Policy, authorize: Policy, user: ?*anyopaque };
    const ProcessOptions = extern struct { executable: [*c]const u8 };
    const BackendOptions = extern struct { struct_size: u32, kind: u32, config: *const anyopaque, config_size: usize };
    const ext_buffer = extern struct { data: [*c]u8, len: usize };
    extern fn ext_host_create(*const ext_host_options, *const BackendOptions, *?*ext_host) ext_status;
    extern fn ext_host_start(?*ext_host, u32) ext_status;
    extern fn ext_host_invoke(?*ext_host, [*c]const u8, usize, u32, *ext_buffer) ext_status;
    extern fn ext_host_destroy(?*ext_host) void;
    extern fn ext_buffer_free(*ext_buffer) void;
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
fn check(s: c.ext_status) !void {
    if (s != c.EXT_OK) return error.EngineFailure;
}
fn read(a: std.mem.Allocator, path: []const u8) ![]u8 {
    const z = try a.dupeSentinel(u8, path, 0);
    const f = c.fopen(z.ptr, "rb") orelse return error.ReadFailed;
    defer _ = c.fclose(f);
    if (c.fseek(f, 0, c.SEEK_END) != 0) return error.ReadFailed;
    const n = c.ftell(f);
    if (n < 0 or n > c.EXT_HOST_MAX_FRAME) return error.ReadFailed;
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
    var options: c.ext_host_options = .{ .abi_version = 2, .struct_size = @sizeOf(c.ext_host_options), .descriptor = descriptor.ptr, .descriptor_len = descriptor.len, .verify = allow, .authorize = allow, .user = null };
    var host: ?*c.ext_host = null;
    try check(c.ext_host_create(&options, &backend, &host));
    defer c.ext_host_destroy(host);
    try check(c.ext_host_start(host, 3000));
    for (args[3..]) |file| {
        const request = try read(a, file);
        var out: c.ext_buffer = .{ .data = null, .len = 0 };
        defer c.ext_buffer_free(&out);
        try check(c.ext_host_invoke(host, request.ptr, request.len, 3000, &out));
        const z = try a.dupeSentinel(u8, out.data[0..out.len], 0);
        _ = c.puts(z.ptr);
    }
}
