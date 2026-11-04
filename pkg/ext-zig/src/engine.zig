//! The C engine's services (`ext_engine_call`), for the rules a guest SDK must not keep a copy of:
//! descriptor and request validation, the schema vocabulary, frames. The engine is compiled from
//! source by build.zig, so there is no library to install.
const std = @import("std");
const ffi = @import("ffi");

pub const Error = error{ Invalid, Denied, Mismatch, Unsupported, NotFound, OutOfMemory, Failed };

pub fn check(status: ffi.ext_status) Error!void {
    return switch (status) {
        ffi.EXT_OK => {},
        ffi.EXT_INVALID => error.Invalid,
        ffi.EXT_DENIED => error.Denied,
        ffi.EXT_MISMATCH => error.Mismatch,
        ffi.EXT_UNSUPPORTED => error.Unsupported,
        ffi.EXT_NOT_FOUND => error.NotFound,
        ffi.EXT_NOMEM => error.OutOfMemory,
        else => error.Failed,
    };
}

/// Runs the named engine service on a JSON input and returns its JSON result, owned by `a`.
pub fn service(a: std.mem.Allocator, operation: [:0]const u8, input: []const u8) Error![]u8 {
    var out: ffi.ext_buffer = .{ .data = null, .len = 0 };
    defer ffi.ext_buffer_free(&out);
    try check(ffi.ext_engine_call(operation.ptr, input.ptr, input.len, &out));
    return a.dupe(u8, if (out.len == 0) &.{} else out.data[0..out.len]) catch error.OutOfMemory;
}

const descriptor = "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"example/echo\",\"revision\":\"1\"}," ++
    "\"contracts\":[{\"name\":\"example.echo\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"}]}]}";

test "the engine is compiled in and answers a service" {
    const a = std.testing.allocator;
    const ok = try service(a, "descriptor.validate", descriptor);
    defer a.free(ok);
    try std.testing.expect(std.mem.indexOf(u8, ok, "example.echo") != null);
    try std.testing.expectError(error.Invalid, service(a, "descriptor.validate", "{\"apiVersion\":\"nope\"}"));
    try std.testing.expectError(error.Unsupported, service(a, "no.such.service", "{}"));
}
