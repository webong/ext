const std = @import("std");
pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const sdk = b.addModule("ctx_plugin", .{ .root_source_file = b.path("src/root.zig"), .target = target, .optimize = optimize, .link_libc = true });
    const tests = b.addTest(.{ .root_module = sdk });
    const run = b.addRunArtifact(tests);
    b.step("test", "Run SDK conformance tests").dependOn(&run.step);
    for ([_][]const u8{ "guest", "host", "shared" }) |name| {
        if (target.result.os.tag == .wasi and !std.mem.eql(u8, name, "guest")) continue;
        const module = b.createModule(.{ .root_source_file = b.path(b.fmt("examples/{s}.zig", .{name})), .target = target, .optimize = optimize, .link_libc = true });
        module.addImport("ctx_plugin", sdk);
        if (std.mem.eql(u8, name, "shared")) {
            b.installArtifact(b.addLibrary(.{ .name = "ctx-zig-guest", .root_module = module, .linkage = .dynamic }));
        } else {
            b.installArtifact(b.addExecutable(.{ .name = b.fmt("ctx-zig-{s}", .{name}), .root_module = module }));
        }
    }
}
