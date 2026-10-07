# Build a plugin once, choose its runtime

The CTX plugin library supports all four runtime families in the comparison:
native Go plugins, HashiCorp RPC subprocesses, WASM/WASI, and C shared libraries.
It also provides in-process endpoints and JSON-line connections. All use the
same descriptors, typed methods, host admission and `ext.plugin/v1` envelopes.
These are runtime implementations in the library; applications and adapters
provide their own domain contracts and trust decisions.

## Authoring workflow

1. Define `author.Method[Input, Output]`, including schemas where needed.
2. Register the handler with `author.Register` and provide guest authorization.
3. Create a guest with `Registry.Guest`.
4. Choose a small entry point for the runtime below.
5. On the host, use `plugin.Open` with reviewed metadata, verification,
   authorization, and the chosen backend's `Open` or `Connect` function.
6. Call the same typed method using `author.Call`.

The [echo implementation](../examples/plugin-runtimes/echo/echo.go) and
[host](../examples/plugin-runtimes/host/main.go) demonstrate this. The echo
handler, schema and descriptor are shared by three build targets; only their
entry points differ. Existing health, configuration and pull-stream contracts
can also run over these backends' unary calls. Reverse host services need an
explicit binding and authorization; loaders do not automatically inject them.

| Runtime | Guest entry point | Host entry point | Availability |
| --- | --- | --- | --- |
| Native Go | Export `CTXPlugin(context.Context) (plugin.Backend, error)` | `nativego.Open(ctx, absolutePath)` | Linux, macOS, FreeBSD; cgo and Go plugin build support required |
| WASI Preview 1 command | `jsonline.ServeStdio(ctx, guest)` | `wasm.Open(ctx, verifiedBytes, options)` | wazero-supported host targets, no cgo required |
| C shared library | Four C exports backed by `pkg/plugin-go/cshared/guest.Server` or a foreign implementation | `cshared.Open(ctx, absolutePath)` | Linux, macOS, FreeBSD, Windows; cgo and a matching native C toolchain required |
| HashiCorp | `hashicorp.Plugin` and go-plugin serving | `hashicorp.Connect` | Existing gRPC and net/rpc bindings; see its [guide](../pkg/plugin-hashicorp/README.md) |

`nativego.Supported()` and `cshared.Supported()` report whether the loader is
compiled into the current build. Unsupported builds return
`plugin.ErrUnsupported` without loading code. They do not claim that an arbitrary
artifact's architecture or dependencies are compatible. The WASI implementation
supports **Preview 1 commands speaking CTX JSON lines**, not every WASM binary,
WASI Preview 2 component or arbitrary exported-function interface.

## Build and use the shared example

Run from the CTX repository root. Use the same Go toolchain, source tree and
build flags for the host and native Go guest. The commands below create local
artifacts and run the example; they do not install anything.

```sh
mkdir -p /tmp/ctx-plugin-example
go build -o /tmp/ctx-plugin-example/host ./examples/plugin-runtimes/host

# WASI: a Go command compiled to wasm.
GOOS=wasip1 GOARCH=wasm go build -o /tmp/ctx-plugin-example/echo.wasm ./examples/plugin-runtimes/wasi
/tmp/ctx-plugin-example/host --backend wasm \
  --artifact /tmp/ctx-plugin-example/echo.wasm \
  --sha256 "$(shasum -a 256 /tmp/ctx-plugin-example/echo.wasm | cut -d ' ' -f 1)"

# Native Go: Linux, macOS or FreeBSD with cgo.
go build -buildmode=plugin -o /tmp/ctx-plugin-example/echo.so ./examples/plugin-runtimes/nativego
/tmp/ctx-plugin-example/host --backend nativego \
  --artifact /tmp/ctx-plugin-example/echo.so \
  --sha256 "$(shasum -a 256 /tmp/ctx-plugin-example/echo.so | cut -d ' ' -f 1)"

# C ABI: this extension is for macOS; use .so on Linux/FreeBSD or .dll on Windows.
go build -buildmode=c-shared -o /tmp/ctx-plugin-example/echo.dylib ./examples/plugin-runtimes/cshared
/tmp/ctx-plugin-example/host --backend cshared \
  --artifact /tmp/ctx-plugin-example/echo.dylib \
  --sha256 "$(shasum -a 256 /tmp/ctx-plugin-example/echo.dylib | cut -d ' ' -f 1)"
```

Each example is intended to print `Hello from CTX`. Use native shell paths and
SHA-256 tooling on Windows. Hashing your own local build is an example trust
decision; computing a digest of a downloaded file does not establish publisher
trust. Production hosts verify reviewed packages and protect artifact paths and
their native dependencies against replacement before and during loading.

## Native Go guests

The entire transport entry point can be:

```go
func CTXPlugin(ctx context.Context) (plugin.Backend, error) {
    guest, err := registry.Guest(author.Options{Authorize: authorize})
    if err != nil {
        return nil, err
    }
    return inprocess.New(guest)
}
```

Define this in `package main` and build with `-buildmode=plugin`. The exported
factory must have exactly that signature. Each factory invocation must return
an independent backend and its `Close` must release any owned resources. For a
guest that owns instance/stream managers, return a backend whose `Close` closes
those too. The factory context bounds startup and must not become the guest's
lifetime context; `plugin.Open` cancels its startup context after the handshake.

Loading runs Go package initializers before the CTX handshake. Native Go has no
isolation from the host. Loader initialization cannot be canceled, handlers
must honor contexts, and `Close` releases resources but does not unload code.
Build the host and plugin with matching toolchains, relevant flags and shared
dependency source. See the [Go plugin documentation](https://pkg.go.dev/plugin)
for upstream build and platform restrictions. Restart the host to fully discard
old native code; use immutable artifact paths for each selected revision.

## WASI guests

Go authors use the existing typed guest and call `jsonline.ServeStdio` from
`main`. The [WASI entry point](../examples/plugin-runtimes/wasi/main.go) also
handles errors on stderr. Guests in other languages implement the existing
JSON-line hello/request/response protocol. Stdout is exclusively protocol data.

`wasm.Open` creates a dedicated wazero runtime and persistent command per
backend. It compiles the supplied verified bytes; CTX does not choose file paths
or grant permissions from module metadata. `plugin.Open` establishes readiness
through the usual exact handshake, so start failures and protocol mismatches
fail admission. The backend lifetime is independent of the startup context.

Defaults and limits:

- At most 64 MiB of module bytes, and 4096 linear memory pages (256 MiB).
- `Options.MemoryLimitPages` may set a different linear memory bound up to
  65536 pages. This does not bound total host/compilation memory or provide fuel
  accounting. The host should also bound concurrent runtimes.
- No inherited environment, filesystem or sockets. `Args`, `Env` and `FS` are
  explicit grants. Clocks, interruptible sleep and system random bytes support
  normal Go/WASI execution. Filesystem implementations must confine access;
  `os.DirFS` alone does not prevent symlink escape.
- Diagnostics are discarded by default. Setting `Stderr` forwards at most
  64 KiB per module by default; `MaxStderrBytes` changes that cap. Diagnostic
  text is guest-controlled. Host writers and filesystem operations must return
  promptly; the runtime cannot forcibly interrupt arbitrary host Go callbacks.
- Calls are serialized. Dispatch cancellation or `Close` closes both protocol
  pipes and terminates WASM execution, including compute loops. There is no
  retry or continuation of that session. `Wait(ctx)` observes actual exit/traps.

The implementation pins wazero v1.10.1 to retain its
[Go 1.23 minimum](https://github.com/tetratelabs/wazero/blob/v1.10.1/go.mod).
The runtime's [configuration reference](https://pkg.go.dev/github.com/tetratelabs/wazero@v1.10.1#RuntimeConfig)
documents linear memory limits and execution cancellation.

## C ABI guests and foreign hosts

The portable contract is [ctx_plugin.h](../pkg/plugin-cshared/ctx_plugin.h).
ABI version `1` is independent of `ext.plugin/v1` and domain contract versions.
Every library must export these C calling-convention symbols:

| Export | Purpose |
| --- | --- |
| `ctx_plugin_abi_version` | Return `1` |
| `ctx_plugin_open` | Create an independent nonzero opaque session handle; return zero on failure |
| `ctx_plugin_call` | Perform handshake (`1`) or invocation (`2`) with bounded JSON buffers |
| `ctx_plugin_close` | Release the handle's resources after calls finish |

The handshake request is `{"deadline":"<RFC3339 timestamp>"}`; its response is
the CTX descriptor. Invocation uses ordinary `plugin.Request`/`Response` JSON.
Handshake must precede invocation. Status zero means a valid response, including
intentional domain failures encoded as `Response.error`. Other statuses are
transport failures; hosts must close the handle and must not replay the call.

The caller allocates request and response memory and owns it throughout.
Responses have exactly `plugin.MaxFrameBytes` (24 MiB) capacity, and the guest
reports the written length. There is no buffer-resize retry that could repeat
side effects. Guests must not retain/free caller pointers, overrun buffers, or
let exceptions cross the C boundary. Host and guest may use different languages
and allocators. The Go host uses C-allocated byte buffers; Go pointers and Go
objects never cross between independently compiled runtimes.

For Go, copy the [four cgo wrappers](../examples/plugin-runtimes/cshared/main.go)
and use `guest.New(factory, options)`. The helper owns handles, enforces capacity
(64 by default), serializes each handle, validates envelopes and applies
deadlines. It deliberately requires an explicit factory so each handle owns
its backend's resources. `CallInto` checks lengths; callers still must supply
valid native pointers. cgo's generated header declares the Go exports; foreign
implementations can use the portable header directly. Both have the same ABI.
A foreign host can load the Go-built library and use these four exports too;
that host must implement CTX selection, validation and admission policy itself.

Rust authors can use `ctx_plugin::export_guest!(factory)` from the
[Rust SDK](../pkg/plugin-rust/README.md). Zig authors can use
`comptime { sdk.cabi.exportGuest(factory); }` from the
[Zig SDK](../pkg/plugin-zig/README.md). These helpers implement serialization,
validation, dispatch and handle/buffer ownership around typed guest methods.
Both SDKs also serve command guests over JSON lines and compile them to WASI.
They include host Sessions for consuming CTX guests from those languages.

For other languages, implement the four exports in the header and the CTX JSON
envelopes. A library with unrelated C symbols does not become a CTX guest just
because the host can load its file. Run `scripts/plugin-crosslang.sh` for the
Rust/Zig/Go interoperability suite; see each SDK's guide for toolchain setup.

Native code has full host-process access. The host can stop waiting on a canceled
call, but it cannot forcibly stop that code. Cancellation closes admission;
cleanup happens after the active native call returns. `Close` initiates this
cleanup and `WaitClosed(ctx)` waits for actual handle disposal. A guest that
ignores deadlines can keep work/resources alive, and a native fault can crash
the host. No loaded library is unloaded, including on ABI failure, because its
initializers may already have created runtimes or callbacks. This lifetime rule
applies to all C ABI guests, including Go `c-shared` libraries.

## Package metadata

Declare the runtime from the backend profile:

- `nativego`
- `wasm/wasi-preview1`
- `cshared`
- Existing `inprocess`, `jsonline`, `hashicorp/grpc`, `hashicorp/netrpc`

All currently advertise `ext.plugin/v1`. Add supported profiles explicitly to
`packagekit.Environment.Runtimes`; `SelectEntrypoint` preflights a named entry
point before launching it. Register native profiles only when `Supported()` is
true. Native artifacts should declare host `os` and `arch`. A portable WASI
artifact omits those fields: its `wasip1/wasm` compilation target describes the
guest, while the manifest's platform fields constrain the host.

The core does not select a backend by product name or infer one from an extension.
Native paths, installation and policy belong to the consuming application or
adapter. Xallet and Cymonkey adoption remain separate work.
