//! The download fixtures every binding runs: lib/plugin/testdata/v1/download-cases.json,
//! served from a local HTTPS origin with a certificate made for the test
//! (testdata/origin.py, which needs python3 and openssl), beside a plain HTTP server
//! that must never receive a request. The test skips without the fixtures or python3.
const std = @import("std");
const engine = @import("ext_plugin_engine");
const fixtures = @import("build_options").fixtures;
const download = engine.download;
const Io = std.Io;

const libc = struct {
    extern fn mkdtemp([*c]u8) ?[*:0]u8;
};

fn text(value: std.json.Value, key: []const u8) ?[]const u8 {
    const found = value.object.get(key) orelse return null;
    return if (found == .string) found.string else null;
}

const Origin = struct {
    child: std.process.Child,
    scratch: []const u8,
    origin: []const u8,
    plain: []const u8,
    cert: []const u8,

    fn start(a: std.mem.Allocator, io: Io, cases_path: []const u8) !Origin {
        var template = "/tmp/ext-zig-download-XXXXXX".*;
        const scratch = try a.dupe(u8, std.mem.span(libc.mkdtemp(&template) orelse return error.SkipZigTest));
        errdefer Io.Dir.cwd().deleteTree(io, scratch) catch {};
        var child = std.process.spawn(io, .{
            .argv = &.{ "python3", "-I", @import("build_options").origin, cases_path, scratch },
            .stdin = .ignore,
            .stdout = .pipe,
            .stderr = .inherit,
        }) catch return error.SkipZigTest;
        errdefer child.kill(io);
        var buffer: [1024]u8 = undefined;
        var reader = child.stdout.?.reader(io, &buffer);
        const line = reader.interface.takeDelimiterExclusive('\n') catch return error.OriginDidNotStart;
        const info = try std.json.parseFromSliceLeaky(std.json.Value, a, line, .{});
        return .{
            .child = child,
            .scratch = scratch,
            .origin = try a.dupe(u8, text(info, "origin").?),
            .plain = try a.dupe(u8, text(info, "plain").?),
            .cert = try std.fs.path.join(a, &.{ scratch, "origin.pem" }),
        };
    }
    fn stop(self: *Origin, io: Io) void {
        self.child.kill(io);
        Io.Dir.cwd().deleteTree(io, self.scratch) catch {};
    }
    fn url(self: *const Origin, a: std.mem.Allocator, template: []const u8) ![]const u8 {
        const once = try std.mem.replaceOwned(u8, a, template, "{origin}", self.origin);
        return std.mem.replaceOwned(u8, a, once, "{plain}", self.plain);
    }
};

fn outcome(result: download.DownloadError!void) []const u8 {
    result catch |err| return switch (err) {
        error.Invalid => "invalid",
        else => "failed",
    };
    return "ok";
}

fn exists(io: Io, path: []const u8) bool {
    Io.Dir.cwd().access(io, path, .{}) catch return false;
    return true;
}

test "every download case of the shared fixture" {
    const io = std.testing.io;
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const a = arena.allocator();
    const cases_path = try std.fs.path.join(a, &.{ fixtures, "download-cases.json" });
    const data = Io.Dir.cwd().readFileAlloc(io, cases_path, a, .limited(1 << 20)) catch return error.SkipZigTest;
    const fixture = try std.json.parseFromSliceLeaky(std.json.Value, a, data, .{});
    try std.testing.expectEqual(@as(i64, download.max_redirects), fixture.object.get("maxRedirects").?.integer);
    const cases = fixture.object.get("cases").?.array.items;
    try std.testing.expect(cases.len != 0);

    var origin = try Origin.start(a, io, cases_path);
    defer origin.stop(io);
    var downloader = download.Downloader.init(std.testing.allocator, io);
    defer downloader.deinit();
    try downloader.trustOnly(origin.cert);
    const landing_path = try std.fs.path.join(a, &.{ origin.scratch, "landing.json" });

    for (cases, 0..) |case, i| {
        const name = text(case, "name").?;
        const destination = try std.fmt.allocPrint(a, "{s}/case-{d}", .{ origin.scratch, i });
        const existed = if (case.object.get("destinationExists")) |v| v.bool else false;
        if (existed) try Io.Dir.cwd().writeFile(io, .{ .sub_path = destination, .data = "existing" });
        var headers: std.ArrayList(std.http.Header) = .empty;
        if (case.object.get("headers")) |given| {
            var it = given.object.iterator();
            while (it.next()) |entry| try headers.append(a, .{ .name = entry.key_ptr.*, .value = entry.value_ptr.string });
        }
        const max_bytes: u64 = if (case.object.get("maxBytes")) |v| @intCast(v.integer) else 0;
        Io.Dir.cwd().deleteFile(io, landing_path) catch {};
        const got = outcome(downloader.download(try origin.url(a, text(case, "url").?), destination, max_bytes, headers.items));
        const want = text(case, "expect").?;
        if (!std.mem.eql(u8, want, got)) {
            std.debug.print("{s}: got {s}, want {s}\n", .{ name, got, want });
            return error.TestUnexpectedResult;
        }
        if (std.mem.eql(u8, got, "ok")) {
            const body = try Io.Dir.cwd().readFileAlloc(io, destination, a, .limited(1 << 20));
            try std.testing.expectEqualStrings(text(case, "body").?, body);
        } else if (existed) {
            const body = try Io.Dir.cwd().readFileAlloc(io, destination, a, .limited(1 << 20));
            try std.testing.expectEqualStrings("existing", body);
        } else if (exists(io, destination)) {
            std.debug.print("{s}: a failed download left {s}\n", .{ name, destination });
            return error.TestUnexpectedResult;
        }
        if (case.object.get("absentAtLanding")) |absent| {
            const landing_data = try Io.Dir.cwd().readFileAlloc(io, landing_path, a, .limited(1 << 20));
            const landing = try std.json.parseFromSliceLeaky(std.json.Value, a, landing_data, .{});
            for (absent.array.items) |header| {
                const lower = try std.ascii.allocLowerString(a, header.string);
                if (landing.object.get(lower) != null) {
                    std.debug.print("{s}: {s} followed the redirect\n", .{ name, header.string });
                    return error.TestUnexpectedResult;
                }
            }
        }
    }
    const plain_hit = try std.fs.path.join(a, &.{ origin.scratch, "plain-hit" });
    if (exists(io, plain_hit)) {
        std.debug.print("the plain HTTP server received a request\n", .{});
        return error.TestUnexpectedResult;
    }

    // An archive downloaded under the store's limit is installed and its download removed.
    const cases_text = try Io.Dir.cwd().readFileAlloc(io, try std.fs.path.join(a, &.{ fixtures, "archive-cases.json" }), a, .limited(4 << 20));
    const archive_cases = try std.json.parseFromSliceLeaky(std.json.Value, a, cases_text, .{});
    const first = archive_cases.object.get("cases").?.array.items[0].object.get("steps").?.array.items[0];
    try std.testing.expectEqualStrings("archives/tar-gz-top.bin", text(first, "install").?);
    const sha256 = try a.dupeSentinel(u8, text(first, "sha256").?, 0);
    const root = try std.fmt.allocPrintSentinel(a, "{s}/plugins", .{origin.scratch}, 0);
    var store = try engine.Store.init(root, .{});
    defer store.deinit();
    const archive_url = try origin.url(a, "https://{origin}/archives/tar-gz-top.bin");
    try std.testing.expectError(error.Mismatch, downloader.install(&store, archive_url, "0000000000000000000000000000000000000000000000000000000000000000", null, .{}));
    var manifest = try downloader.install(&store, archive_url, sha256, "example/archive", .{});
    manifest.deinit();
    var receipt = try store.receipt("example/archive");
    defer receipt.deinit();
    try std.testing.expect(std.mem.indexOf(u8, receipt.bytes(), sha256) != null);
    try std.testing.expectError(error.Invalid, downloader.install(&store, try origin.url(a, "http://{plain}/payload"), sha256, null, .{}));
    var small = try engine.Store.init(try std.fmt.allocPrintSentinel(a, "{s}/small", .{origin.scratch}, 0), .{ .max_archive_bytes = 16 });
    defer small.deinit();
    try std.testing.expectError(error.Invalid, downloader.install(&small, archive_url, sha256, null, .{}));
}
