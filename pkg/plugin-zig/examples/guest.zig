const std = @import("std");
const sdk = @import("ext_plugin");
const common = @import("common.zig");
pub fn main() !void {
    var guest = try common.factory(std.heap.page_allocator);
    defer guest.deinit();
    var stdio: sdk.jsonline.Stdio = .{};
    var conn = stdio.connection();
    try sdk.jsonline.serve(&guest, &conn, std.heap.page_allocator);
}
