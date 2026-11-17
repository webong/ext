//! Downloading a package archive over HTTPS, for `Store.installArchive` to
//! install. The engine has no HTTPS, so the binding fetches the file with
//! `std.http.Client` and the engine checks it against the pinned digest.
//!
//! The rules are those of every binding (lib/plugin/testdata/v1/download-cases.json):
//! only `https` URLs with a host and without user information or a fragment, for the
//! first request and for every redirect; at most `max_redirects` redirects;
//! `Authorization`, `Cookie` and `Referer` are not sent after a redirect; the body is
//! no larger than the limit, whatever `Content-Length` says; and a download that
//! fails leaves no file and never changes one that existed. The content is not
//! verified here: `Store.installArchive` checks it against the pinned digest
//! before anything is extracted.
const std = @import("std");
const root = @import("root.zig");
const Io = std.Io;
const http = std.http;

/// How many redirects a download follows.
pub const max_redirects = 5;
/// The body limit when none is given: 512 MiB, the store's default archive limit.
pub const default_max_bytes: u64 = 512 << 20;
/// Headers that never follow a redirect.
const private_headers = [_][]const u8{ "authorization", "cookie", "referer" };

/// Why a download failed. `Invalid` is a refusal by the rules (a URL that is
/// not allowed, too many redirects or a body over the limit), as the engine's
/// `error.Invalid`; `HttpStatus` is an origin that answered other than 200;
/// `DestinationExists` a destination that is already there; `Transfer` any
/// other failure of the connection, the TLS session or the file.
pub const DownloadError = error{ Invalid, HttpStatus, DestinationExists, Transfer, OutOfMemory, Canceled };
/// The failures of `Downloader.install`: those of the download and the engine's.
pub const InstallError = DownloadError || root.Error;

/// An HTTPS client that follows the download rules. `init` trusts the system's
/// root certificates, read at the first request; `trustOnly` replaces them with
/// the PEM certificates of one file, for a private origin. There is no proxy and
/// no timeout: cancel the `Io` operation to bound a download. Not thread safe.
pub const Downloader = struct {
    client: http.Client,

    pub fn init(gpa: std.mem.Allocator, io: Io) Downloader {
        return .{ .client = .{ .allocator = gpa, .io = io } };
    }
    pub fn deinit(self: *Downloader) void {
        self.client.deinit();
    }
    /// Trusts only the PEM certificates in the file at `absolute_path`.
    pub fn trustOnly(self: *Downloader, absolute_path: []const u8) !void {
        const now = Io.Clock.real.now(self.client.io);
        var bundle: std.crypto.Certificate.Bundle = .empty;
        errdefer bundle.deinit(self.client.allocator);
        try bundle.addCertsFromFilePathAbsolute(self.client.allocator, self.client.io, now, absolute_path);
        self.client.ca_bundle.deinit(self.client.allocator);
        self.client.ca_bundle = bundle;
        self.client.now = now;
    }

    /// Fetches `url` into the new file `destination` (an absolute path that must
    /// not exist) and fails when the body is larger than `max_bytes`
    /// (`default_max_bytes` when zero). `headers` go with the first request;
    /// `Authorization`, `Cookie` and `Referer` among them are dropped at the first
    /// redirect, the others are kept. Nothing is left at `destination` on failure.
    pub fn download(self: *Downloader, url: []const u8, destination: []const u8, max_bytes: u64, headers: []const http.Header) DownloadError!void {
        const limit = if (max_bytes == 0) default_max_bytes else max_bytes;
        const gpa = self.client.allocator;
        const io = self.client.io;
        try checkUrl(url);
        var current = try gpa.dupe(u8, url);
        defer gpa.free(current);
        var kept: std.ArrayList(http.Header) = .empty;
        defer kept.deinit(gpa);
        for (headers) |header| {
            if (!isPrivate(header.name)) try kept.append(gpa, header);
        }
        var redirects: usize = 0;
        while (true) {
            const uri = std.Uri.parse(current) catch return error.Invalid;
            var request = self.client.request(.GET, uri, .{
                .redirect_behavior = .unhandled,
                .keep_alive = false,
                .headers = .{ .authorization = .omit, .accept_encoding = .{ .override = "identity" } },
                .extra_headers = if (redirects == 0) headers else kept.items,
            }) catch |err| return transfer(err);
            defer request.deinit();
            request.sendBodiless() catch |err| return transfer(err);
            var response = request.receiveHead(&.{}) catch |err| return transfer(err);
            const code = @backingInt(response.head.status);
            switch (code) {
                301, 302, 303, 307, 308 => {
                    const location = response.head.location orelse return error.Invalid;
                    const next = try resolve(gpa, current, location);
                    errdefer gpa.free(next);
                    try checkUrl(next);
                    redirects += 1;
                    if (redirects > max_redirects) return error.Invalid;
                    gpa.free(current);
                    current = next;
                    continue;
                },
                200 => {},
                else => return error.HttpStatus,
            }
            if (response.head.content_length) |declared| {
                if (declared > limit) return error.Invalid;
            }
            return save(io, &response, destination, limit);
        }
    }

    /// Downloads `url` under the store's archive limit into a new private
    /// directory of the system's temporary directory, installs it with
    /// `Store.installArchive` and removes the download, whatever happened.
    pub fn install(self: *Downloader, store: *const root.Store, url: []const u8, sha256_hex: [:0]const u8, id: ?[:0]const u8, options: root.StoreOptions) InstallError!root.Buffer {
        const temporary = if (libc.getenv("TMPDIR")) |dir| std.mem.span(dir) else "/tmp";
        var template_buffer: [4096]u8 = undefined;
        const template = std.fmt.bufPrintSentinel(&template_buffer, "{s}/ext-download-XXXXXX", .{std.mem.trimEnd(u8, temporary, "/")}, 0) catch return error.Transfer;
        const scratch = std.mem.span(libc.mkdtemp(template.ptr) orelse return error.Transfer);
        defer Io.Dir.cwd().deleteTree(self.client.io, scratch) catch {};
        var path_buffer: [4096]u8 = undefined;
        const archive = std.fmt.bufPrintSentinel(&path_buffer, "{s}/archive", .{scratch}, 0) catch return error.Transfer;
        try self.download(url, archive, store.max_archive_bytes, &.{});
        return store.installArchive(archive, sha256_hex, id, options);
    }
};

const libc = struct {
    extern fn getenv([*c]const u8) ?[*:0]const u8;
    extern fn mkdtemp([*c]u8) ?[*:0]u8;
};

fn isPrivate(name: []const u8) bool {
    for (private_headers) |private| {
        if (std.ascii.eqlIgnoreCase(name, private)) return true;
    }
    return false;
}

fn transfer(err: anyerror) DownloadError {
    return switch (err) {
        error.OutOfMemory => error.OutOfMemory,
        error.Canceled => error.Canceled,
        error.UnsupportedUriScheme, error.UriMissingHost, error.InvalidHostName => error.Invalid,
        else => error.Transfer,
    };
}

/// Writes the body into a new file, removing it on any failure.
fn save(io: Io, response: *http.Client.Response, destination: []const u8, limit: u64) DownloadError!void {
    var file = Io.Dir.cwd().createFile(io, destination, .{ .exclusive = true, .permissions = .fromMode(0o600) }) catch |err| return switch (err) {
        error.PathAlreadyExists => error.DestinationExists,
        error.Canceled => error.Canceled,
        else => error.Transfer,
    };
    var ok = false;
    defer if (!ok) Io.Dir.cwd().deleteFile(io, destination) catch {};
    defer file.close(io);
    var transfer_buffer: [64]u8 = undefined;
    const body = response.reader(&transfer_buffer);
    var write_buffer: [16384]u8 = undefined;
    var writer = file.writer(io, &write_buffer);
    var chunk: [16384]u8 = undefined;
    var total: u64 = 0;
    while (true) {
        const n = body.readSliceShort(&chunk) catch return error.Transfer;
        total += n;
        if (total > limit) return error.Invalid;
        writer.interface.writeAll(chunk[0..n]) catch return error.Transfer;
        if (n < chunk.len) break;
    }
    writer.interface.flush() catch return error.Transfer;
    file.sync(io) catch return error.Transfer;
    ok = true;
}

/// The scheme, authority and the rest of an absolute URL.
const Parts = struct { scheme: []const u8, authority: []const u8, rest: []const u8 };
fn split(url: []const u8) ?Parts {
    const at = std.mem.indexOf(u8, url, "://") orelse return null;
    const rest = url[at + 3 ..];
    const end = std.mem.indexOfAny(u8, rest, "/?#") orelse rest.len;
    return .{ .scheme = url[0..at], .authority = rest[0..end], .rest = rest[end..] };
}

/// Checks a URL against the rules: `https`, a host, no user information and no fragment.
pub fn checkUrl(url: []const u8) DownloadError!void {
    for (url) |c| if (c <= ' ' or c == 0x7f) return error.Invalid;
    const parts = split(url) orelse return error.Invalid;
    if (!std.ascii.eqlIgnoreCase(parts.scheme, "https")) return error.Invalid;
    if (std.mem.indexOfScalar(u8, parts.authority, '@') != null) return error.Invalid;
    if (std.mem.indexOfScalar(u8, url, '#') != null) return error.Invalid;
    var host = parts.authority;
    if (std.mem.lastIndexOfScalar(u8, host, ':')) |colon| {
        if (std.mem.indexOfScalar(u8, host[colon..], ']') == null) host = host[0..colon];
    }
    if (host.len == 0) return error.Invalid;
    const uri = std.Uri.parse(url) catch return error.Invalid;
    if (uri.host == null or uri.host.?.isEmpty()) return error.Invalid;
}

/// Resolves a redirect's `Location` against the URL that answered it. Dot
/// segments are left for the origin to interpret.
pub fn resolve(gpa: std.mem.Allocator, base: []const u8, location: []const u8) DownloadError![]u8 {
    if (std.mem.indexOfScalar(u8, location, ':')) |colon| {
        const scheme = location[0..colon];
        var valid = scheme.len != 0 and std.ascii.isAlphabetic(scheme[0]);
        for (scheme) |c| {
            if (!std.ascii.isAlphanumeric(c) and c != '+' and c != '-' and c != '.') valid = false;
        }
        if (valid) return gpa.dupe(u8, location);
    }
    const parts = split(base) orelse return error.Invalid;
    if (std.mem.startsWith(u8, location, "//")) return std.fmt.allocPrint(gpa, "{s}:{s}", .{ parts.scheme, location });
    if (std.mem.startsWith(u8, location, "/")) return std.fmt.allocPrint(gpa, "{s}://{s}{s}", .{ parts.scheme, parts.authority, location });
    const path = parts.rest[0 .. std.mem.indexOfAny(u8, parts.rest, "?#") orelse parts.rest.len];
    if (location.len == 0 or location[0] == '#') return std.fmt.allocPrint(gpa, "{s}://{s}{s}{s}", .{ parts.scheme, parts.authority, parts.rest, location });
    if (location[0] == '?') return std.fmt.allocPrint(gpa, "{s}://{s}{s}{s}", .{ parts.scheme, parts.authority, path, location });
    const directory = if (std.mem.lastIndexOfScalar(u8, path, '/')) |end| path[0 .. end + 1] else "/";
    return std.fmt.allocPrint(gpa, "{s}://{s}{s}{s}", .{ parts.scheme, parts.authority, directory, location });
}

test "resolves locations" {
    const base = "https://a.example:8443/x/y?q";
    const cases = [_][2][]const u8{
        .{ "/p", "https://a.example:8443/p" },
        .{ "p", "https://a.example:8443/x/p" },
        .{ "?r", "https://a.example:8443/x/y?r" },
        .{ "//b.example/p", "https://b.example/p" },
        .{ "http://b.example/p", "http://b.example/p" },
        .{ "file:///etc/hosts", "file:///etc/hosts" },
    };
    for (cases) |case| {
        const got = try resolve(std.testing.allocator, base, case[0]);
        defer std.testing.allocator.free(got);
        try std.testing.expectEqualStrings(case[1], got);
    }
}

test "checks urls" {
    try checkUrl("https://a.example/p");
    try checkUrl("https://[::1]:8443/p");
    for ([_][]const u8{
        "http://a.example/p",
        "https:///p",
        "https://:443/p",
        "https://u@a.example/p",
        "https://a.example/p#f",
        "https://a.example/p q",
        "file:///etc/hosts",
        "a.example/p",
    }) |url| try std.testing.expectError(error.Invalid, checkUrl(url));
}
