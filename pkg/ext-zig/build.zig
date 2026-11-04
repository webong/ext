const std = @import("std");

/// The portable core of the C engine, as `core_sources` names it in lib/plugin/CMakeLists.txt:
/// what a guest needs, with no host, filesystem or process code. Zig compiles it for any target,
/// WASI included, so the SDK needs neither CMake nor a prebuilt library.
const core_sources = [_][]const u8{
    "src/common.c",   "src/wire.c",  "src/progress.c", "src/reverse.c",  "src/endpoint.c",
    "src/services.c", "src/guest.c", "src/sha256.c",   "src/packages.c", "vendor/yyjson.c",
};

/// The flags the engine is compiled with. Every function the vendored parser declares is renamed
/// `ext_host_private_<name>`, declarations and definitions alike, so linking the engine into an
/// application that has a parser of its own cannot collide: `lib/plugin/CMakeLists.txt` does the
/// same with a generated header, and the vendored source stays unmodified.
fn parserFlags(b: *std.Build, engine_dir: []const u8) []const []const u8 {
    const path = b.pathJoin(&.{ engine_dir, "vendor/yyjson.h" });
    const source = std.Io.Dir.cwd().readFileAlloc(b.graph.io, path, b.allocator, .limited(16 << 20)) catch |err|
        std.debug.panic("cannot read {s}: {s}", .{ path, @errorName(err) });
    var flags: std.ArrayList([]const u8) = .empty;
    flags.appendSlice(b.allocator, &.{ "-std=c11", "-Wall", "-Wextra", "-DEXT_HOST_STATIC", "-Dyyjson_api=" }) catch @panic("OOM");
    var lines = std.mem.splitScalar(u8, source, '\n');
    while (lines.next()) |line| {
        if (!std.mem.startsWith(u8, line, "yyjson_api ")) continue;
        // The first identifier followed by an opening parenthesis names the function.
        const open = std.mem.indexOfScalar(u8, line, '(') orelse @panic("unrecognized parser declaration");
        var start = open;
        while (start > 0 and (std.ascii.isAlphanumeric(line[start - 1]) or line[start - 1] == '_')) start -= 1;
        if (start == open) @panic("unrecognized parser declaration");
        const name = line[start..open];
        flags.append(b.allocator, b.fmt("-D{s}=ext_host_private_{s}", .{ name, name })) catch @panic("OOM");
    }
    return flags.items;
}

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const engine_dir = b.option([]const u8, "engine-src", "Directory of the C engine sources (default ../../lib/plugin)") orelse "../../lib/plugin";

    const include: std.Build.LazyPath = .{ .cwd_relative = b.pathJoin(&.{ engine_dir, "include" }) };
    const engine = b.createModule(.{ .target = target, .optimize = optimize, .link_libc = true });
    engine.addCSourceFiles(.{
        .root = .{ .cwd_relative = engine_dir },
        .files = &core_sources,
        .flags = parserFlags(b, engine_dir),
    });
    engine.addIncludePath(include);
    engine.addIncludePath(.{ .cwd_relative = b.pathJoin(&.{ engine_dir, "src" }) });
    engine.addIncludePath(.{ .cwd_relative = b.pathJoin(&.{ engine_dir, "vendor" }) });
    const engine_lib = b.addLibrary(.{ .name = "ext_guest", .linkage = .static, .root_module = engine });

    b.step("engine", "Compile the C engine core for the target").dependOn(&engine_lib.step);

    const ffi = b.addTranslateC(.{ .root_source_file = b.path("src/ffi.h"), .target = target, .optimize = optimize });
    ffi.addIncludePath(include);

    const sdk = b.addModule("ext_io", .{ .root_source_file = b.path("src/root.zig"), .target = target, .optimize = optimize, .link_libc = true });
    sdk.addImport("ffi", ffi.createModule());
    sdk.linkLibrary(engine_lib);
    const tests = b.addTest(.{ .root_module = sdk });
    const run = b.addRunArtifact(tests);
    b.step("test", "Run SDK conformance tests").dependOn(&run.step);
    for ([_][]const u8{ "guest", "frames", "capability", "capability-host", "host", "shared" }) |name| {
        if (target.result.os.tag == .wasi and (std.mem.eql(u8, name, "host") or std.mem.eql(u8, name, "shared") or std.mem.eql(u8, name, "capability-host"))) continue;
        const module = b.createModule(.{ .root_source_file = b.path(b.fmt("examples/{s}.zig", .{name})), .target = target, .optimize = optimize, .link_libc = true });
        module.addImport("ext_io", sdk);
        if (std.mem.eql(u8, name, "shared")) {
            b.installArtifact(b.addLibrary(.{ .name = "ext-zig-guest", .root_module = module, .linkage = .dynamic }));
        } else {
            b.installArtifact(b.addExecutable(.{ .name = b.fmt("ext-zig-{s}", .{name}), .root_module = module }));
        }
    }
}
