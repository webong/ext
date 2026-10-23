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

The [C ABI](../../lib/plugin/include/ext_plugin.h) passes only borrowed byte buffers. A foreign
caller owns them and supplies 24 MiB response capacity. Zig does not return
allocator-owned memory or free caller memory. Valid non-overlapping pointers
are required. Panics cannot be recovered by this helper and can terminate the
host when running natively.

## Report progress and call the host

A guest served by `jsonline.serve` (a command or a WASI module, not the C ABI) can report
progress while a call runs and call back into its host. An operation that reports declares
`.progress = true`; the host operations a guest may call are declared with
`Registry.declareReverse`. Both go through the call's `Context`:

```zig
fn handle(ctx: *sdk.author.Context, _: sdk.wire.Request, input: sdk.wire.Value) !sdk.wire.Value {
    // Events reach the host in order, before the result. .dropped is routine: the rate limit the
    // host set dropped it and counts it for the next event.
    _ = try ctx.progress(sdk.frames.Progress.log("starting"));
    _ = try ctx.progress(sdk.frames.Progress.fraction(1, 3));
    // The host's refusal or error is left in ctx.reverse_error and returned as error.Remote
    // (denied, busy, too_large, expired, ...). The answer lives as long as ctx.allocator.
    return ctx.reverse(.{ .name = "host.svc", .version = "v1" }, "sign", input);
}
```

`ctx.progress` fails with `error.Unsupported` for an operation that did not declare progress, a
call the host asked no events of, or a transport with no frames (stop reporting),
`error.Invalid` for an event that breaks the rules (nothing is sent), and `error.Closed` once the
call is over. `ctx.progressLimits()` is the rate the host set, and `frames.splitText` cuts a long
line into events that each fit. This guest reads *inline*: it has no reader of its own, so the
thread that waits for the host's answer reads the connection itself. The host sends nothing but
that answer during a call, so this suffices, with limits: one reverse call at a time, no
session-scoped caller, and a wait that ends with the answer or the end of the connection (standard
input cannot be read with a timeout). A frame the host may not send ends the session with
`error.Protocol` and the call in flight is not answered; when the input ends during a call,
`ctx.reverse` fails with `error.Closed`, the call is answered and the session ends. `Guest.invoke`
on its own has no frames, so `progress` and `reverse` fail with `error.Unsupported` there.

`src/frames_tests.zig` reproduces the shared fixtures
(`lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases,model-cases}.json`, computed by the Go
contract) and runs the router against a scripted host; `examples/frames.zig` is the frames
conformance guest of [docs/plugin-guest-frames.md](../../docs/plugin-guest-frames.md), which
`scripts/plugin-crosslang.sh` runs natively and as a WASI command through the Go session. It
answers the operations that need a reader of its own with the public error `unsupported`. The
fixtures are read from `EXT_FIXTURES`, or relative to the build root or the repository root.

Requests are decoded with `wire.requestFromValue`, which reads a request the way the Go contract
does (a member it does not know is refused, a null member is empty); `wire.descriptorFromValue`
does the same for a descriptor.

**Status:** written without a Zig toolchain, then compiled and run with Zig 0.17.0: the 17 SDK tests pass
(the three fixtures and the scripted router among them), and the frames suite passes natively through
the Go session and the C engine, and as a WASI command through the Go session.

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

`examples/common.zig` contains the shared conformance guest, and `examples/frames.zig` the frames one. Native builds
produce command/host executables and a C shared library. WASI builds produce
only the command guest. The native integration runner targets Linux and macOS;
Windows native hosting and other Zig versions are not validated by this pass.

Parsing rejects duplicate keys (including escaped aliases), unknown envelope
fields, invalid UTF-8, trailing JSON, excessive depth and oversized frames.
Raw JSON numbers are retained as text to avoid losing precision; typed methods
choose their numeric types. Use decimal strings for values that must remain
exact when exchanged with JavaScript peers.
