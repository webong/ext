# WAMR reactor backend

An optional backend for the shared plugin engine that runs a WebAssembly
[reactor](../../../docs/plugin-reactor-abi.md) in process, on
[WAMR](https://github.com/bytecodealliance/wasm-micro-runtime)'s interpreter. It is the
fallback route for mobile hosts that cannot use the platform web engine (see the
[decision record](../../../docs/adr-mobile-wasm.md)), and it works on desktop too. It is
**off by default**: nothing in the engine changes unless `EXT_WITH_WAMR` is set.

```c
ext_backend_extension_context ext;
ext_wamr_backend_create(module_bytes, module_len, &options, &ext);
ext_backend_options backend = {sizeof(backend), EXT_BACKEND_EXTENSION_CONTEXT, &ext, sizeof(ext)};
ext_host_create(&host_options, &backend, &host);   /* then ext_host_start, ext_host_invoke ... */
```

See [`ext_wamr.h`](ext_wamr.h) for the options. Build and test it with
`scripts/plugin-wamr.sh`, which fetches WAMR at the pinned commit, builds the Rust SDK's
conformance reactor, and runs the test.

## What it enforces

- **Only the ABI's WASI imports.** The host provides `clock_time_get`, `random_get`,
  `environ_sizes_get`, `environ_get`, `fd_write` (descriptors 1 and 2, diagnostics only),
  `poll_oneoff` (clock subscriptions only) and `proc_exit` (a trap), and nothing else. WAMR's
  own WASI is off, because it would hand the guest the host's standard input. WAMR links an
  unknown import lazily and only warns, so the backend checks every import after loading and
  refuses a module that imports anything it does not provide, naming it.
- **A reactor, not a command.** A module exporting `_start`, or missing any `ext_plugin_*`
  export, is refused.
- **Deadlines, cancel and close interrupt a running guest**, from a watchdog that sleeps
  unless a call is running (no idle wake-ups). A guest that is stopped, traps, exits or
  runs out of memory ends the whole instance, as the ABI says; later calls return
  `EXT_CLOSED`.
- **Memory is capped** per instance (`memory_limit_pages`, default 4096 pages) and an
  optional instruction budget applies to each call into the guest (`instruction_limit`).

| Outcome | Status |
| --- | --- |
| deadline passed | `EXT_TIMEOUT` |
| `ext_cancel` signalled | `EXT_CANCELED` |
| instruction budget exceeded | `EXT_TIMEOUT` |
| guest allocation failed or memory cap hit | `EXT_NOMEM` |
| trap, `proc_exit`, closed, or a session already ended | `EXT_CLOSED` |
| malformed request | `EXT_INVALID`, and the session stays usable |

The backend only returns statuses from `EXT_OK` through `EXT_CANCELED`, because the engine's
host layer (`extension_call` in `src/host.c`) rewrites anything above that range, such as
`EXT_CAPACITY` or `EXT_SEQUENCE`, to `EXT_INVALID`. An earlier version returned
`EXT_CAPACITY` for an exhausted instruction budget, and applications saw "invalid request";
a test now checks the status through `ext_host_invoke`.

A guest that honours the request deadline (the conformance reactor's `wait` does) may
answer with its own deadline error just before the host's watchdog fires, so a test of an
overrunning call should accept either a timeout or that error response.
| module refused (not a reactor, forbidden import, does not load) | `EXT_UNSUPPORTED`, with the reason sent to `diagnostic` |

## Building it

WAMR is not vendored. `EXT_WAMR_ROOT` points at a checkout; `scripts/plugin-wamr.sh` pins
commit `f5f57c09aee623436f5fb87a90798fdd2cdf39fd`, the one the options below were
measured at. WAMR is Apache-2.0 with the LLVM exception; if you ship a binary that links
it, ship its license.

The CMake file turns on, and these are all required:

- **`WAMR_BUILD_INSTRUCTION_METERING` and `WAMR_BUILD_THREAD_MGR`.** Without them a looping
  guest cannot be stopped at all: `wasm_runtime_terminate` does nothing without the thread
  manager.
- **`WAMR_BUILD_REF_TYPES` and `WAMR_BUILD_BULK_MEMORY`.** Current Rust and C toolchains
  emit both by default, and a reactor built with them does not load without these.
- The fast interpreter only: no JIT or ahead-of-time code, which iOS forbids.

One option is off by default and matters for embedding: **`EXT_WAMR_DISABLE_HW_BOUND_CHECK`**.
WAMR checks guest memory bounds with signal handlers unless told otherwise. A process that
installs its own handlers, and a JVM does, so every Android app, can abort on them: the
Android binding of this backend aborted with SIGILL at first use until it turned this on.
It makes WAMR check bounds in software, which is slower but safe in such a process. The test
passes either way; the speed difference was not measured.

Android needs `-DANDROID_PLATFORM=android-28` or newer: the engine's subprocess backend
(`src/host.c`) uses `posix_spawn`, which Android provides from API 28. The CPU comes from
`CMAKE_OSX_ARCHITECTURES`, `ANDROID_ABI` or the processor name, and only arm64 and x86_64
are supported.

## What was verified

Run, with the Rust SDK's conformance reactor (id `ctx/conformance`, contract
`ext.conformance` v1), `scripts/plugin-wamr.sh` and `tests/wamr_test.c`:

| Where | Result |
| --- | --- |
| macOS 15 on Apple silicon, native | full test passes |
| macOS, AddressSanitizer and UBSan | no memory errors; UBSan reports misaligned loads, all inside WAMR's interpreter (it relies on unaligned access), none in `ext_wamr.c` |
| macOS, ThreadSanitizer | no reports (it has a watchdog thread and a concurrent `close`) |
| iOS 18.3 Simulator, arm64, minimum iOS 15 | full test passes, run inside the simulator |
| Android 14 emulator, arm64-v8a, API 28 | full test passes, run on the emulator |
| GCC 15 at `-O3 -Werror`, with and without `NDEBUG` | `ext_wamr.c` and the test compile clean (this is the compiler CI uses) |

The test covers: handshake; echo with non-ASCII text and an exact 20-digit number; a 1 MiB
frame; a domain error and a malformed request; 200 repeated calls; a deadline stopping a
sleeping guest, after which the instance is gone; cancel; `close` from another thread
during a call; the instruction budget; the memory cap; a command module, a module with a
forbidden import and garbage bytes; 20 create-and-destroy cycles; and the whole path
through `ext_host_create`, `ext_host_start` and `ext_host_invoke`.

The iOS Simulator and Android rows are from before the status fix below; after it only macOS
was re-run, since the change is to which status values are returned and not to anything
platform-specific.

Timings on all three: a deadline of 300 ms stopped the guest at about 300 ms, cancel and
close at about 200 ms after being requested.

Measured on macOS only (not a phone), with the 343 KB reactor: connecting (load,
instantiate, handshake) took 34 to 43 ms, a call's median was about 0.4 ms (99th percentile
3 to 7 ms), and the process grew to 12 MB, because the ABI's 24 MiB response buffer is not
committed until the guest writes to it.

## What was not verified

- **Real phones.** Nothing has run on a physical iOS or Android device, so phone speed,
  memory and thermal behaviour are unknown. The interpreter is much slower than a JIT.
- **Linux and x86_64.** Not built here, so CI's GCC build of the engine has not run with
  this option; the option is off by default so CI is unaffected. Windows is unsupported.
- **A security review.** The import set and memory cap are enforced and tested, but the
  guest runs inside WAMR's interpreter and its own sandbox, and nobody has reviewed that
  for escapes.
- **Large frames and many sessions.** Only up to 1 MiB was tried, and one session per
  backend.
- **Not wired into the language SDKs.** The Swift and Java SDKs do not use this backend
  yet.
