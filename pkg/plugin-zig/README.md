# CTX plugin SDK for Zig

This package implements typed guests and host sessions for `ext.plugin/v1`,
including JSON-line and C ABI bindings. It targets **Zig 0.17.0**, pinned because
Zig's standard library and build APIs change between releases. It has no
third-party dependencies and links libc for clocks and standalone stdio.

Add a local path dependency in your `build.zig.zon`, then import its named module:

```zig
const ctx = b.dependency("ext_plugin", .{ .target = target, .optimize = optimize });
exe.root_module.addImport("ext_plugin", ctx.module("ext_plugin"));
```

The dependency path points to CTX's `pkg/plugin-zig` directory. A future published
package can use the same module name; no registry publication is included here.

## Typed authoring

```zig
const sdk = @import("ext_plugin");
const Message = struct { text: []const u8 };
const echo: sdk.author.Method(Message, Message) = .{
    .contract = .{ .name = "example.echo", .version = "v1" },
    .operation = .{ .name = "echo", .surface = "observation" },
    .validate_input = validate,
};
fn validate(input: Message) !void {
    if (input.text.len > 256) return error.Invalid;
}
fn handle(ctx: *sdk.author.Context, _: sdk.wire.Request, input: Message) !Message {
    try ctx.check();
    return input;
}
fn authorize(request: sdk.wire.Request) !void {
    // Apply domain policy here; this local example permits the echo operation.
    if (!sdk.wire.eq(u8, request.operation, "echo")) return error.Denied;
}
pub fn factory(a: sdk.wire.Allocator) !sdk.author.Guest {
    var registry = try sdk.author.Registry.init(a, .{
        .id = "example/echo", .revision = "build-1",
    });
    defer registry.deinit();
    try registry.register(Message, Message, echo, handle);
    return registry.guest(authorize);
}
```

Methods/handlers are registered at compile time with typed input/output and
optional validators. JSON payload structs reject unknown fields. Domain bounds
belong in validators; this package does not implement the separate
`ext.schema/v1` interpreter. Runtime domain state can be managed by the
application; the initial dispatcher API uses function pointers, not closures.

`Registry.guest` deep-copies its declaration and entries into an owned arena.
Release it once with `Guest.deinit`; registry allocations can be released as
soon as the guest is created. Request/response trees are allocated in per-call
arenas. Do not retain their slices after the call; duplicate retained data into
your own allocator. The types owning arenas/connections must not be copied and
then independently deinitialized.

## Guest entry points

- Command or WASI: use `jsonline.Stdio.connection()` and
  `jsonline.serve(&guest, &connection, allocator)`. Stdout is protocol-only.
- C shared library: `comptime { sdk.cabi.exportGuest(factory); }` supplies the
  four CTX ABI exports. The factory creates an independent guest per handle.
  The helper owns/deinitializes guests, admits up to 64 handles and serializes
  ABI calls. Its callback work must honor `Context.check()` deadlines.
- Return `ctx.fail(.{ .code = "busy", .message = "try later" })` for a deliberate
  public error. Other handler errors are sanitized to `operation_failed`.

The [C ABI](../../pkg/plugin-cshared/ext_plugin.h) passes only borrowed byte buffers. A foreign
caller owns them and supplies 24 MiB response capacity. Zig does not return
allocator-owned memory or free caller memory. Valid non-overlapping pointers
are required. Panics cannot be recovered by this helper and can terminate the
host when running natively.

## Host API

`host.Session.open(allocator, selected, verify, connector, authorize)` snapshots
the selection, verifies before connecting, and checks the full handshake.
Policies are required. `Session.call(Input, Output, allocator, method, input)`
returns either `.value` or `.remote`, preserving structured public errors.
`callRaw` accepts explicit contract/operation/JSON and a timeout in nanoseconds.
Returned values live in the caller's arena. Close/deinit a session once; these
are serial APIs requiring external synchronization if shared across threads.

- `host.CShared.open(absolute_path)` supplies a native backend on platforms
  supported by Zig's libc dynamic loader. Its image remains resident.
- `host.JSONLine` binds `jsonline.Connection` to the Session contract. Supply
  `jsonline.IO` callbacks for reads, writes, closure and enforced I/O deadlines.
- A custom `host.Backend` receives ABI operation `1` (deadline-bearing hello)
  or `2` (CTX request), and returns descriptor/response JSON.

**Cancellation is cooperative in the Zig host bindings.** C calls and the
standalone libc stdio helper are synchronous. The host checks deadlines before
and after a call but cannot forcibly interrupt native execution or a blocking
libc read. For bounded IPC waits, supply deadline-aware `IO` callbacks; for
untrusted execution, use a host that can terminate the WASI module/process.
The Go host's WASI/C backends retain their existing cancellation behavior when
hosting Zig guests. No retry, installation, discovery or native-code unloading
is implicit.

## Build and test

```sh
zig build test --build-file pkg/plugin-zig/build.zig
zig build --build-file pkg/plugin-zig/build.zig
zig build --build-file pkg/plugin-zig/build.zig -Dtarget=wasm32-wasi --prefix /tmp/ext-zig-wasi
scripts/plugin-crosslang.sh
```

`examples/common.zig` contains the shared conformance guest. Native builds
produce command/host executables and a C shared library. WASI builds produce
only the command guest. The native integration runner targets Linux and macOS;
Windows native hosting and other Zig versions are not validated by this pass.

Parsing rejects duplicate keys (including escaped aliases), unknown envelope
fields, invalid UTF-8, trailing JSON, excessive depth and oversized frames.
Raw JSON numbers are retained as text to avoid losing precision; typed methods
choose their numeric types. Use decimal strings for values that must remain
exact when exchanged with JavaScript peers.
