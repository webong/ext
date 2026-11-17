//! The archive fixtures every store runs, in every language:
//! lib/plugin/testdata/v1/archive-cases.json with the archives beside it. They are
//! outside this package, so the test skips when it is built without the repository.
const std = @import("std");
const engine = @import("ext_plugin_engine");
const fixtures = @import("build_options").fixtures;

const libc = struct {
    extern fn mkdtemp([*c]u8) ?[*:0]u8;
    extern fn system([*c]const u8) c_int;
};

fn status(result: engine.Error!void) []const u8 {
    result catch |err| return switch (err) {
        error.Exists => "exists",
        error.NotFound => "not_found",
        error.Mismatch => "mismatch",
        error.Unsupported => "unsupported",
        error.Invalid => "invalid",
        else => "failed",
    };
    return "ok";
}

fn text(value: std.json.Value, key: []const u8) ?[]const u8 {
    const found = value.object.get(key) orelse return null;
    return if (found == .string) found.string else null;
}

fn limit(case: std.json.Value, key: []const u8) u64 {
    const limits = case.object.get("limits") orelse return 0;
    const found = limits.object.get(key) orelse return 0;
    return @intCast(found.integer);
}

fn z(a: std.mem.Allocator, value: []const u8) ![:0]const u8 {
    return a.dupeSentinel(u8, value, 0);
}

/// The string at `path` (object keys) in the JSON `bytes`.
fn pointer(a: std.mem.Allocator, bytes: []const u8, path: []const []const u8) ![]const u8 {
    var value = try std.json.parseFromSliceLeaky(std.json.Value, a, bytes, .{});
    for (path) |key| value = value.object.get(key) orelse return error.TestUnexpectedResult;
    return value.string;
}

test "every archive case of the shared fixture" {
    const io = std.testing.io;
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const cases_path = try std.fs.path.join(a, &.{ fixtures, "archive-cases.json" });
    const data = std.Io.Dir.cwd().readFileAlloc(io, cases_path, a, .limited(4 << 20)) catch return error.SkipZigTest;
    const fixture = try std.json.parseFromSliceLeaky(std.json.Value, a, data, .{});
    const cases = fixture.object.get("cases").?.array.items;
    try std.testing.expect(cases.len != 0);

    for (cases) |case| {
        const name = text(case, "name").?;
        var template = "/tmp/ext-zig-archive-XXXXXX".*;
        const base = std.mem.span(libc.mkdtemp(&template) orelse return error.SkipZigTest);
        defer _ = libc.system((z(a, std.fmt.allocPrint(a, "rm -rf '{s}'", .{base}) catch unreachable) catch unreachable).ptr);
        const root = try std.fmt.allocPrint(a, "{s}/plugins", .{base});
        var store = try engine.Store.init(try z(a, root), .{
            .max_archive_bytes = limit(case, "maxArchiveBytes"),
            .max_tree_files = @intCast(limit(case, "maxTreeFiles")),
            .max_tree_bytes = limit(case, "maxTreeBytes"),
            .max_artifact_bytes = limit(case, "maxArtifactBytes"),
        });
        defer store.deinit();

        for (case.object.get("steps").?.array.items, 0..) |step, i| {
            const sha256 = text(step, "sha256");
            var got_revision: []const u8 = "";
            var got: []const u8 = undefined;
            if (text(step, "install")) |archive| {
                const path = try std.fs.path.join(a, &.{ fixtures, archive });
                const id: ?[:0]const u8 = if (text(step, "id")) |v| try z(a, v) else null;
                if (store.installArchive(try z(a, path), try z(a, sha256 orelse ""), id, .{})) |manifest| {
                    var owned = manifest;
                    defer owned.deinit();
                    got_revision = try pointer(a, owned.bytes(), &.{ "descriptor", "identity", "revision" });
                    got = "ok";
                } else |err| got = status(err);
            } else if (text(step, "receipt")) |id| {
                if (store.receipt(try z(a, id))) |receipt| {
                    var owned = receipt;
                    defer owned.deinit();
                    try std.testing.expectEqualStrings(id, try pointer(a, owned.bytes(), &.{"id"}));
                    try std.testing.expectEqualStrings(sha256.?, try pointer(a, owned.bytes(), &.{"artifact_sha256"}));
                    got_revision = try pointer(a, owned.bytes(), &.{"revision"});
                    got = "ok";
                } else |err| got = status(err);
            } else if (text(step, "remove")) |id| {
                got = status(store.remove(try z(a, id)));
            } else if (text(step, "damage")) |id| {
                const directory = try std.mem.replaceOwned(u8, a, id, "/", "%2f");
                const path = try std.fs.path.join(a, &.{ root, directory, text(step, "path").? });
                try std.Io.Dir.cwd().writeFile(io, .{ .sub_path = path, .data = "damaged" });
                got = "ok";
            } else {
                std.debug.print("{s} step {d}: nothing to do\n", .{ name, i });
                return error.TestUnexpectedResult;
            }
            const want = text(step, "status") orelse "ok";
            const want_revision = text(step, "revision") orelse "";
            if (!std.mem.eql(u8, want, got) or !std.mem.eql(u8, want_revision, got_revision)) {
                std.debug.print("{s} step {d}: got {s} {s}, want {s} {s}\n", .{ name, i, got, got_revision, want, want_revision });
                return error.TestUnexpectedResult;
            }
        }
        // Whatever happened, nothing is left under the root but packages and receipts.
        if (std.Io.Dir.cwd().openDir(io, root, .{ .iterate = true })) |opened| {
            var dir = opened;
            defer dir.close(io);
            var entries = dir.iterate();
            while (try entries.next(io)) |entry| {
                if (entry.name.len != 0 and entry.name[0] == '.' and !std.mem.eql(u8, entry.name, ".receipts")) {
                    std.debug.print("{s}: left {s} in the store\n", .{ name, entry.name });
                    return error.TestUnexpectedResult;
                }
            }
        } else |_| {}
        if (std.Io.Dir.cwd().access(io, "/tmp/ext-archive-escape", .{})) |_| {
            std.debug.print("{s}: an archive wrote outside the store\n", .{name});
            return error.TestUnexpectedResult;
        } else |_| {}
    }
}
