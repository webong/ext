const std = @import("std");
pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const include = b.option([]const u8, "engine-include", "Installed C engine headers") orelse "../../../lib/plugin/include";
    const lib = b.option([]const u8, "engine-lib", "C engine library directory") orelse @panic("provide -Dengine-lib=/absolute/path");
    const shared = b.option(bool, "shared", "Link the optional shared engine") orelse false;
    const translated = b.addTranslateC(.{ .root_source_file = b.path("src/bindings.h"), .target = target, .optimize = optimize });
    translated.addIncludePath(.{ .cwd_relative = include });
    const module = b.addModule("ext_plugin_engine", .{ .root_source_file = b.path("src/root.zig"), .target = target, .optimize = optimize, .link_libc = true });
    module.addImport("ffi", translated.createModule());
    module.addLibraryPath(.{ .cwd_relative = lib });
    module.addRPath(.{ .cwd_relative = lib });
    module.linkSystemLibrary(if (shared) "ext_host" else "ext_host_static", .{});
    module.linkSystemLibrary("pthread", .{});
    module.linkSystemLibrary("m", .{});
    // The shared-library backend loads guests with dlopen, which older glibc keeps in libdl.
    if (!shared and target.result.os.tag == .linux) module.linkSystemLibrary("dl", .{});
    const tests = b.addTest(.{ .root_module = module });
    const run = b.addRunArtifact(tests);
    const step = b.step("test", "Check the C engine binding");
    step.dependOn(&run.step);
    // The archive and download fixtures every binding runs. They are outside this
    // package, so the tests skip when the directory is not there.
    const fixtures = b.option([]const u8, "fixtures", "Directory of lib/plugin/testdata/v1") orelse (b.root.joinString(b.allocator, "../../../lib/plugin/testdata/v1") catch @panic("OOM"));
    const options = b.addOptions();
    options.addOption([]const u8, "fixtures", fixtures);
    options.addOption([]const u8, "origin", b.root.joinString(b.allocator, "testdata/origin.py") catch @panic("OOM"));
    for ([_][]const u8{ "src/archive_tests.zig", "src/download_tests.zig" }) |path| {
        const fixture_module = b.createModule(.{ .root_source_file = b.path(path), .target = target, .optimize = optimize, .link_libc = true });
        fixture_module.addImport("ext_plugin_engine", module);
        fixture_module.addOptions("build_options", options);
        step.dependOn(&b.addRunArtifact(b.addTest(.{ .root_module = fixture_module })).step);
    }
}
