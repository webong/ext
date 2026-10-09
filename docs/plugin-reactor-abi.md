# The WebAssembly reactor ABI

A plugin guest that runs inside a WebAssembly host, a phone's web engine or an
embedded interpreter, is a **reactor**: a module that exports the `ext.plugin` C ABI
([`ext_plugin.h`](../pkg/plugin-cshared/ext_plugin.h)) plus two allocation functions.
It never reads standard input and never loops forever. The host calls its exports
when it has a frame to deliver, so no Worker, `SharedArrayBuffer` or cross-origin
isolation is involved. [The mobile decision record](adr-mobile-wasm.md) explains why
this is the common guest format; this page is the contract.

A reactor is the same plugin as a shared-library guest. Source written against
`ext_plugin.h` compiles to either target, and the same conformance suite checks both.

## Exports

| Export | Signature (WebAssembly) | Meaning |
|---|---|---|
| `memory` | linear memory | The memory the host reads and writes. Required. |
| `ext_plugin_abi_version` | `() -> i32` | `1`. |
| `ext_plugin_open` | `() -> i64` | Create an independent session. `0` is failure. |
| `ext_plugin_call` | `(i64 handle, i32 operation, i32 request, i32 request_len, i32 response, i32 capacity, i32 written) -> i32` | One handshake or invocation; see below. |
| `ext_plugin_close` | `(i64 handle) -> ()` | Release a session. |
| `ext_plugin_alloc` | `(i32 size) -> i32` | Allocate `size` bytes in guest memory. Returns a 16-byte-aligned pointer, or `0` on failure or when `size` is `0`. |
| `ext_plugin_free` | `(i32 pointer, i32 size) -> ()` | Release memory from `ext_plugin_alloc`, with the same size. |
| `_initialize` | `() -> ()` | Optional. If present, the host calls it exactly once, first. |

A module that exports `_start` is a command, not a reactor, and a host must refuse it.
The six `ext_plugin_*` functions are required; a host refuses a module missing any.

`ext_plugin_open`, `ext_plugin_call` and `ext_plugin_close` have the semantics, status
codes and constants of `ext_plugin.h`: operation `1` is the handshake, `2` is an
invocation, and the statuses are `0` ok, `1` invalid, `2` closed, `3` failed. Requests
and responses are UTF-8 JSON without a trailing NUL or newline. Read that header for
the rules; they are not repeated here. Only the memory model below is new.

## Memory model

- A pointer is an unsigned 32-bit offset into `memory`. Values are little-endian.
  `handle` is a 64-bit integer, which a JavaScript host passes as a `BigInt`.
- The host owns every buffer it passes. It allocates them with `ext_plugin_alloc`,
  writes into them, and frees them with `ext_plugin_free`.
- For each call the host allocates the request buffer, and frees it afterwards. It
  allocates **one response buffer of `24 MiB` (`EXT_PLUGIN_MAX_FRAME_BYTES`)** and one
  4-byte cell for `written` per session, and reuses them. The capacity is always the
  maximum, because a call may have performed effects and cannot be retried with a
  larger buffer. A guest returns `1` (invalid) if the capacity is not the maximum.
- On status `0` the guest has stored the response length in the `written` cell, and
  the host reads that many bytes from the response buffer. On any other status it
  must not read the response.
- **Memory can grow during a call.** A JavaScript host must read `memory.buffer`
  again after every call into the guest, because growing detaches the old buffer.
- The guest never retains or frees a host pointer, and never writes beyond
  `capacity`.

## Execution model

- An instance is single-threaded and runs one call at a time. The host serializes all
  calls into an instance, across sessions, and a guest must not assume otherwise. The
  C ABI's "different handles may run at once" does not apply here.
- A guest must not block on input and must not sleep unboundedly. It honours the
  request deadline it was given, as any guest does.
- **A trap ends the instance.** If the guest traps, exhausts memory, or the host
  cancels it, the host discards the whole instance and every session in it, and
  reports a transport failure. Cancellation of a call that overruns its deadline is
  exactly that: the host closes the module. A web host with no way to interrupt a
  running call destroys the page.
- A failed `ext_plugin_open`, `ext_plugin_alloc` or `ext_plugin_call` leaves nothing
  for the host to free except what it allocated itself.

## WASI imports

A reactor may import functions from `wasi_snapshot_preview1`, because standard
libraries pull them in. A host provides the set below and nothing else; a module
importing anything outside it fails to instantiate.

| Import | What a host does |
|---|---|
| `clock_time_get` | Provide wall and monotonic time. |
| `random_get` | Provide random bytes from a secure source. |
| `environ_sizes_get`, `environ_get` | Report an empty environment. |
| `fd_write` | Accept writes to descriptors 1 and 2 as diagnostics; discard or log them, bounded. Refuse other descriptors. |
| `poll_oneoff` | Support clock subscriptions only, for sleeping. A host without a way to sleep may busy-wait. |
| `proc_exit` | Treat as a trap. |

There is no filesystem, network or standard input. This is the set the Rust SDK's
shared guest imports today; a guest that needs more is outside this ABI.

## Producing a reactor

The Rust SDK's `export_guest!` macro emits the allocation exports on `wasm32`:

```sh
cargo build --release --target wasm32-wasip1 --example shared
```

Any language that compiles to `wasm32` and can export these functions works. The
module needs no `_start`. Compiling `ext_plugin.h`-style C needs a bump allocator or
`malloc` behind `ext_plugin_alloc`.

## Hosts and what is verified

| Host | Status |
|---|---|
| `wasm.OpenReactor` in `pkg/plugin-wasm`, on wazero | Implemented. The Rust SDK's shared guest, built as a reactor, passes the full cross-language conformance suite through it: round trips, concurrent calls, public and private errors, deadline cancellation, close while a call is blocked. |
| A hidden `WKWebView`, `WebViewPluginHost` in `pkg/plugin-ios` | Implemented over the page API of `res/web/bundle` (`shim.js`). The Rust reactor passes its 9 tests on macOS and in the iOS 18.3 Simulator: handshake, echo with Unicode and large numbers, public and sanitized private errors, ordering, a guest that honours its deadline, and destroying the page when a call overruns. Not run on a physical device. |
| A hidden Android `WebView`, `WebViewPluginHost` in `pkg/plugin-android` | Implemented in plain Java over the same page API and the same glue. The Rust reactor passes the same 9 checks in a real `WebView` on an Android 14 (API 34) emulator; not run on a physical device. |
| WAMR inside `pkg/plugin-engine` (optional, `EXT_WITH_WAMR`) | Implemented as the fallback route in [`pkg/plugin-engine/wamr`](../pkg/plugin-engine/wamr/README.md). The Rust SDK's conformance reactor passes its test on macOS, in the iOS 18.3 Simulator and on an Android 14 emulator; deadline, cancel, close, memory and instruction limits all stop a guest. It needs metering and the thread manager on in WAMR to meet the deadline rules. Not run on a physical device. |
| `WamrPluginHost` in `pkg/plugin-ios` and `pkg/plugin-android` (optional) | Bindings over the WAMR backend above, built only when the WAMR xcframework or `EXT_WAMR_ROOT` is supplied. The same 12 checks pass on macOS and in the iOS 18.3 Simulator (Swift), and on a desktop JDK and an Android 14 emulator (Java). Inside a JVM or ART the build disables WAMR's hardware bound checks, which otherwise abort the JVM. Not run on a physical device. |
| wasmi inside `pkg/plugin-engine` | Not built; the alternative if startup or memory matter more than avoiding a Rust build. |

### Memory

The 24 MiB response buffer is not committed up front: a page only pays for what the guest
writes. Measured with the Rust conformance reactor, once each, on a Mac and on an
emulator, not on a phone:

| | Before | Reactor loaded, handshake done | After a 4 MB echo |
|---|---|---|---|
| macOS `WKWebView` content process, footprint | 10 MB (bare page) | 20 MB | 47 MB |
| Android 14 emulator, WebView renderer process, PSS | none | 36.9 MB | 138.6 MB |
| Android 14 emulator, app process, PSS | 23.0 MB (no WebView yet) | 60.4 MB | 46.0 MB |

What this means:

- The buffer is not what costs. A 24 MiB buffer would put the macOS process over 34 MB
  and the Android renderer over 60 MB if it were committed, and neither is.
- **Payload size is what costs.** A frame passes through the page as a JSON string, UTF-8
  bytes, guest memory and the guest's own parsing, so one 4 MB frame cost about 27 MB on
  macOS and about 100 MB on the Android renderer, many times its size. The 24 MiB frame
  limit is therefore not practical through the web-engine route on a phone; keep frames
  small, or use a native backend for large data.
- Part of the Android app-process figure is the WebView itself, which a bare WebView would
  also pay; a bare-WebView baseline was not measured, so the reactor's share of it is
  unknown. Footprint and PSS are different metrics and the figures are not comparable
  across rows.

The first web view in a freshly launched app was slow to start in the simulator: 32 s
cold, then 11.7 s, then 3.1 s as it warmed. A host should give `start` a generous
timeout; device cold start is unmeasured. If it matters, a later ABI version can let a
guest own the response buffer. Version 1 keeps the C ABI's caller-owned buffers so a
single source compiles to both targets.
