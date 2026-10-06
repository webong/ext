const std = @import("std");
pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const include = b.option([]const u8, "engine-include", "Installed C engine headers") orelse "../../../pkg/plugin-engine/include";
    const lib = b.option([]const u8, "engine-lib", "C engine library directory") orelse @panic("provide -Dengine-lib=/absolute/path");
    const shared = b.option(bool, "shared", "Link the optional shared engine") orelse false;
    const translated = b.addTranslateC(.{ .root_source_file = b.path("src/bindings.h"), .target = target, .optimize = optimize });
    translated.addIncludePath(.{ .cwd_relative = include });
    const module = b.addModule("ctx_plugin_engine", .{ .root_source_file = b.path("src/root.zig"), .target = target, .optimize = optimize, .link_libc = true });
    module.addImport("ffi", translated.createModule());
    module.addLibraryPath(.{ .cwd_relative = lib });
    module.addRPath(.{ .cwd_relative = lib });
    module.linkSystemLibrary(if (shared) "ctx_host" else "ctx_host_static", .{});
    module.linkSystemLibrary("pthread", .{});
    module.linkSystemLibrary("m", .{});
    const tests = b.addTest(.{ .root_module = module });
    const run = b.addRunArtifact(tests);
    b.step("test", "Check the C engine binding").dependOn(&run.step);
}
