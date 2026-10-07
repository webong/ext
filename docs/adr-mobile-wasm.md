# Hosting WebAssembly plugins on iOS and Android

Status: proposed. Evidence below is marked **run** (executed here), **read** (taken
from a project's own README on 2026-10-08) or **unverified**.

## The question

A Swift or Kotlin app that hosts third-party ext plugins cannot load downloaded
native code: iOS and Google Play forbid it (**unverified**: the exact store rules
were not read). Interpreted WebAssembly is the usual way around that. The shared C
engine in `pkg/plugin-engine` has no WebAssembly runtime, and `pkg/plugin-wasm` uses
wazero, which is Go. What should mobile hosts use?

## Option 1: a C-callable interpreter in `pkg/plugin-engine`

Every binding (Swift, Kotlin, the rest) would get it. Candidates, from their READMEs:

| | wasm3 | WAMR | wasmi |
| --- | --- | --- | --- |
| Language | C | C | Rust |
| License | MIT | Apache-2.0 with LLVM exception | Apache-2.0 |
| C interface | native | native | official Wasm C API (`wasmi_c_api_impl`) |
| Platforms named | Android, iOS | Android (iOS not stated) | `no_std`, embedded |
| Limits | gas metering, suspend/resume | metering not stated | built-in fuel metering |
| WASI | "many WASI apps" | WASI or a built-in libc | WASI Preview 1 (`wasmi_wasi`) |
| Footprint | about 64 KB code | about 57 to 59 KB interpreters | not stated |
| Maintenance | **entering a minimal maintenance phase**, no new features | active | active |

All three were pushed to in the last few weeks and none is archived (**read**, from
the GitHub API). wasm3's own notice makes it the weakest long-term bet. wasmi has the
clearest answer to the engine's cancellation and deadline rules (fuel), at the cost
of a Rust build step, which the repository already has. WAMR is the C-native choice,
but its README states neither iOS support nor metering.

### What running WAMR and wasmi showed (2026-10-08)

WAMR (shallow clone, fast interpreter, no JIT, WASI) and wasmi 2.0.0 were built on
this Mac and given the same inputs. They are **run** results on desktop, not on a
phone.

| | WAMR | wasmi 2.0.0 |
| --- | --- | --- |
| Real guest (the Go WASI plugin guest, 5.6 MB, JSON lines on stdio) | passes, 7 of 7 runs | passes, 7 of 7 runs |
| Handshake, median | 123 ms | 55 ms |
| One call, median | 1.2 ms | 1.6 ms |
| Peak memory | 56 MB | 33 MB |
| Stops an infinite loop that does no I/O | yes, by instruction metering (`wasm_runtime_set_instruction_count_limit`, a build option) and by `wasm_runtime_terminate` from another thread (needs the thread manager built in) | yes, by fuel through the library (`Config::consume_fuel`, `Store::set_fuel`); see the caveat below |
| Embedded from C | **run**: a C program loaded a module, set a limit and called it; runtime library 563 KB | the official Wasm C API exists (**read**); **not exercised** |
| Build | CMake, C | Cargo, Rust |
| iOS and Android | `product-mini/platforms/ios` (Xcode project generator) and `android` (with a JNI wrapper) exist in the source (**read**); neither was built | not built |

Findings that change how to use them:

- **WAMR's limits are build options, not defaults.** Without
  `WAMR_BUILD_INSTRUCTION_METERING` there is no metering, and without
  `WAMR_BUILD_THREAD_MGR` an asynchronous terminate did not stop a running loop at
  all (the loop ran until it was killed). Both stopped the loop once enabled:
  terminate fired 4 ms after its 300 ms request.
- **wasmi's `--fuel` command-line flag is unreliable in 2.0.0.** The same infinite
  loops were never stopped by it, and fuel use was not reported, although wasmi
  1.1.0's flag stopped them in about 35 ms. The library API in 2.0.0 stops them
  correctly, so this is the command-line wrapper, not the engine. Do not use the CLI
  as a safety check.
- **Fuel is not comparable across major versions.** The same million-iteration loop
  costs 3 fuel per iteration in wasmi 1.1.0 and 9 in 2.0.0, because 2.0.0 reworked
  metering. A host that sets budgets must pin the version.
- **Both run our unmodified guest.** The Go test guest imports sixteen WASI calls
  and both interpreters supplied them, so a command-style guest works on either.
  Only standard input and output were exercised; files and clocks beyond what Go's
  runtime needs were not.

**Not done:** an iOS or Android build of either interpreter, anything on a device,
memory limits, and the wasmi C API.

## Option 3: the system web engine

Every phone has one that runs WebAssembly: WKWebView on iOS, the System WebView on
Android. A spike (**run**) put a real plugin guest, the repository's Go test guest
compiled to WASI (5.6 MB), inside a page and spoke the real `ext.plugin/v1` protocol
to it: handshake, an `echo` with a Unicode payload, and an unknown operation that
was correctly refused as `invalid_request`.

| Where it ran | Instantiate | Handshake | Echo |
| --- | --- | --- | --- |
| Chrome (macOS) | 575 ms | 143 ms | 4 ms |
| Safari (macOS) | 169 ms | 198 ms | 10 ms |
| Firefox (macOS) | 263 ms | 132 ms | 3 ms |
| Mobile Safari, iOS 18.3 Simulator | 248 ms | 200 ms | 13 ms |
| Chrome, Android 14 emulator | 567 ms | 611 ms | 11 ms |

The code is `examples/plugin-webview`. What this does and does not show:

- It shows a WASI plugin guest works in WebKit, Blink and Gecko, with no interpreter
  shipped. It is a proof of concept: the WASI shim covers only the sixteen calls this
  guest imports.
- It does **not** show behaviour on a real device. The simulator runs on the Mac's
  CPU and the emulator is not a phone, so speed and any JIT restrictions on real
  hardware are unmeasured.
- It does **not** show behaviour inside an app. The spike used a browser fetching
  from a loopback server. An app's WKWebView or WebView loads content differently,
  and whether it can be cross-origin isolated there is **unverified** (below).

### The guest model is the real constraint

This guest is a **command**: it exports only `_start` and loops reading standard
input. A browser's main thread cannot block waiting for input, so the spike ran it
in a Worker whose standard input is a ring buffer in a `SharedArrayBuffer`, read with
`Atomics.wait`. Browsers allow `SharedArrayBuffer` only on cross-origin-isolated
pages, which is why the web engine gained an opt-in `CrossOriginIsolation` option.
That is fragile inside an app's embedded webview, where the page is not served over
ordinary HTTP.

The plugin C ABI (`pkg/plugin-cshared/ext_plugin.h`) is the other shape: a handful
of synchronous functions (`ext_plugin_open`, `ext_plugin_call`, `ext_plugin_close`)
with caller-owned buffers. A guest compiled to WebAssembly that **exports** those
functions (a reactor) needs no blocking input, no Worker and no isolation. The page,
or any interpreter, just calls the exports. This is a design direction: **no reactor
guest was built or run**, and the ABI needs a buffer-allocation convention for
WebAssembly memory, which it does not define.

## Recommendation

1. Define a WebAssembly reactor guest: the existing `ext_plugin_*` exports plus an
   allocation export. It is the common denominator, because every option can host it.
2. Use the system web engine as the zero-dependency baseline for app hosts. It ships
   nothing, and the spike shows the hard part works. Its isolation is `restricted`.
3. If an in-process `sandboxed` host is wanted in the C engine, prototype it with
   **WAMR first**: it is C built with CMake like the engine, a C embedder worked end
   to end, its limits are proven (with the build options above), and its source tree
   already has iOS and Android platform directories. Keep **wasmi** as the
   alternative if startup time (55 ms against 123 ms), memory (33 MB against 56 MB)
   or deterministic fuel matter more than avoiding a Rust build step. Do not pick
   wasm3 alone because of its maintenance notice. Whichever is chosen, pin its
   version and build options in `pkg/plugin-engine`.
4. Keep the command-style stdio guest as a desktop and server format. It works in
   browsers only with the isolation machinery above.

## Not verified

- Real iOS and Android devices; the store policies; in-app webview isolation and a
  custom-scheme loader.
- Android's System WebView as distinct from Chrome.
- wasmi and WAMR built for iOS and Android, and either running on a device.
- A reactor guest, and the allocation convention it needs.

The Swift and Java bindings belong to the mobile SDK work (`pkg/plugin-swift`,
`pkg/plugin-java`). This record covers only how a mobile host runs WebAssembly.
