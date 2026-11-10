# Hosts and guests in every language

Status: proposed on 2026-11-06, written against `main` at `47f31e50`. It extends
[the SDK switch](plugin-sdk-switch.md), which settled how guest SDKs use the C engine, to the
host side.

Go is not the only language that can run an ext plugin, and it must not be the only one that
can host one. The aim is that any language can write a guest, and any language that can call C
can write a host, without a second engine.

## Where each language stands

| Language | Guest | Host | Host runs on |
| --- | --- | --- | --- |
| Go (`pkg/ext-go`) | Yes, the oracle | Yes: sessions, process, `cshared`, `nativego`, wazero (`wasm`), hashicorp, store, trust | The pure-Go engine, `cgoengine` or `c2goengine` ([selection](adr-engine-selection.md)) |
| Rust (`pkg/ext-rust`) | Yes | `plugin/host.rs`, `plugin/native.rs` (loads C-ABI guests with `libloading`); the engine crate (`pkg/ext-rust/engine`) binds `ext_host` and the store | Its own code, or the C engine through the separate crate |
| Zig (`pkg/ext-zig`) | Yes | `host.zig` over a backend it supplies; the engine package (`pkg/ext-zig/engine`) binds `ext_host` | Its own code, or the C engine through the separate package |
| TypeScript (`pkg/ext-ts`) | Yes | `index.mjs` over a transport (`node.mjs`); the Node addon (`engine/addon.c`) binds `ext_host` | Its own code, or the C engine through the addon |
| Swift (`pkg/ext-ios`) | Yes | WebView and WAMR hosts, store | The C engine |
| Java (`pkg/ext-android`) | Yes | WAMR host, store | The C engine |
| Python (`pkg/ext-python`) | Yes, standard library only | No | |
| Ruby, PHP, Lua (`pkg/ext-ruby`, `pkg/ext-php`, `pkg/ext-lua`) | Yes, no native code | No | |
| .NET (`pkg/ext-dotnet`) | No | Yes, on the C engine (`ExtIO.Host`) | |

Three languages carry a host of their own (Rust, Zig, TypeScript), and each also has a binding
to the C engine's host beside it. Four languages, the scripting ones, cannot host at all.

## What the C engine already hosts

`lib/plugin/include/ext_host.h` is a complete host, not only a validator:

- **Backends.** JSON-line child processes (kinds 1, 4 and 5: explicit argv, a scrubbed
  environment, a working directory, bounded stderr capture), in-process guests the caller
  supplies as callbacks (`EXT_BACKEND_EXTENSION`, `EXT_BACKEND_EXTENSION_CONTEXT`), and
  WebAssembly reactors on WAMR (`lib/plugin/wamr`, built with `EXT_WITH_WAMR`).
- **The connection.** Admission, envelope validation, deadlines, cancellation (`ext_cancel_*`),
  draining, frames, the host half of progress and its rate limit, and reverse calls with
  services the host offers (`ext_host_set_services`).
- **Packages.** Manifest verification, directory and artifact digests, trust records, the
  installed store and the layered store.

A language that binds this header gets all of it. What it adds is what belongs to the language:
how the engine's callbacks reach its threads or event loop, typed sessions, and the capability
and stream clients.

## Decisions

1. **Guests: any language, by one of three routes.**
   - *JSON lines over stdio*, written in the language alone. This is how the Python, Ruby, PHP
     and Lua guests work, and it needs nothing native.
   - *A C-ABI shared library* exporting the four symbols of `ext_plugin.h`: C, C++, Rust, Zig,
     Go, Swift, or anything that can export C functions.
   - *A WebAssembly reactor* ([reactor ABI](plugin-reactor-abi.md)): anything that compiles to it.

2. **Hosts: any language that can call C, by binding the C engine's host.** A new host never
   reimplements admission, validation, progress, reverse calls, packages or trust. A host written
   in its own language would be another copy of the rules [the SDK switch](plugin-sdk-switch.md)
   is removing. The pure-Go engine stays the oracle that the C host is held to.

3. **No new hosts written in their own language.** The Rust, Zig and TypeScript hosts stay until
   their SDKs move onto the engine under the gates and the order the SDK switch already set (Zig,
   then Rust, then TypeScript). Each one's own host then goes in the same change. A gate that fails
   leaves that host as it is, held by the fixtures, as the SDK switch provides.

4. **Scripting languages: the guest stays pure, the host is opt-in.** The guest keeps its promise
   of no native code. The host is a separate part that needs the engine library:

   | Language | Host part | Binds the engine through |
   | --- | --- | --- |
   | Python | `ext_io.host` | `ctypes` and the shared `libext_host` |
   | Lua | `ext_io.host` | a C module (`ext_io.host.core`) that loads the shared `libext_host` |
   | Ruby | `ExtIO::Host` | a native extension (`ExtIO::Host::Native`) that loads the shared `libext_host` and releases the global VM lock around blocking calls; Fiddle is no default gem from Ruby 4.0 |
   | PHP | `ExtIO\Host` | PHP FFI and the shared `libext_host`; policies are safe as FFI callbacks, since the engine runs them on the calling thread (a Zend extension only if that changes) |
   | .NET | `ExtIO.Host` | P/Invoke through unmanaged function pointers that `NativeLibrary` resolves, so each engine binds the library it was given (`LibraryImport` names one library per assembly); a NuGet package like the `ext-dotnet` ones |

   Importing the guest never loads the host, so a guest-only user never needs the library.

5. **No generated wrappers.** Each host is written against `ext_host.h` with the language's own
   foreign-function mechanism, because the work is in callbacks, threads, cancellation and buffer
   ownership, which SWIG and its kind leave to the author.

## What has to exist first

### 1. A shared-library guest backend in the C engine

Today the engine starts processes and runs reactors but cannot load a C-ABI guest. Go
(`cgoengine/cshared`) and Rust (`plugin/native.rs`) each load one themselves, the other hosts
cannot load one at all, and [plugin-runtimes.md](plugin-runtimes.md) tells any other host that it "must
implement CTX selection, validation and admission policy itself". A constructor,
`ext_shared_library_backend_create`, closes that. It returns an `EXT_BACKEND_EXTENSION_CONTEXT`
backend, as the WAMR backend does, so `ext_host_create` and `host.c` do not change:

- it takes an absolute UTF-8 path to a verified library and loads it with `dlopen` or
  `LoadLibraryExW` in connect, so only after the host's verify policy has approved it;
- it checks `ext_plugin_abi_version`, then performs the handshake and invocations through
  `ext_plugin_open`, `ext_plugin_call` and `ext_plugin_close`, with the 24 MiB response capacity
  and the no-replay rule of the ABI;
- it loads each canonical path once and never unloads a library, as the Go loader does not.

It is additive to the ABI. The Go loader is the behaviour it must match: `lib/plugin/tests/shared_library.c`
runs C guests and the Go-built guest that `tests/crosslang` runs through the Go loader. After it
lands, the per-language loaders go when their SDK switches.

### 2. A prebuilt engine library

PHP FFI, Python `ctypes`, Ruby, Lua and .NET need a `libext_host` they can load without CMake.
Each engine release attaches shared and static builds for Linux (x86-64, arm64), macOS (universal)
and Windows (x86-64, arm64), with the headers and SHA-256 sums. No release builds it on its own yet:
the new job reuses the CMake build that `scripts/plugin-cengine-install.sh` and the mobile release
scripts already run, and its tag follows [module versioning](module-versioning.md).

### 3. Host conformance in every language

`tests/crosslang` checks every guest against the Go host, but only one foreign host, Zig's
(`TestCapabilityHosts`), against a Go guest. A host that any language can implement needs a
suite any language can run:

- **The reference guests.** `tests/crosslang/testdata` already builds them: the JSON-line Go
  guest, the frames guest, the shared-library guest and the capability guests.
- **The expected outcomes.** Each case lists what the host must observe: the descriptor, the
  responses and errors, the progress events, the reverse calls and their answers, and the
  refusals. It joins the existing fixtures in `lib/plugin/testdata/v1`.
- **The runner.** Each language's host test runs the guests, compares what it saw with the
  fixture, and writes a result `tests/crosslang` can check. The runner is written once per
  language, and the cases are written once.

A host is not released until it passes this suite on Linux, macOS and Windows.

## Order

1. This record.
2. `ext_shared_library_backend_create` in `lib/plugin`, checked against the guest the Go loader runs.
3. The prebuilt engine library on each engine release.
4. The host conformance cases and the Go runner that shows they are complete.
5. Hosts: Python, then Lua, Ruby, PHP and .NET, each passing the suite before release.
6. The Zig, Rust and TypeScript switches proceed under their own gates; none waits for the others.

## What would change this

- **The shared-library backend cannot match the Go loader's behaviour** on one platform. That
  platform then keeps loading C-ABI guests in each language, and the record says so.
- **A language's foreign-function mechanism cannot carry the engine's callbacks safely**, such as
  PHP FFI with authorization callbacks. That host is then a compiled extension, or the language
  stays guest-only, and the table above says which.
- **Shipping native libraries proves too costly** for a scripting registry. That language's
  host is then built from source only.
