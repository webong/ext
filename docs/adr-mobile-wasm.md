# Hosting WebAssembly plugins on iOS and Android

Status: decided. The system web engine is the primary route for mobile hosts and WAMR
is the fallback (see Decision). Evidence below is marked **run** (executed here), **read** (taken
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
or any interpreter, just calls the exports. The contract, including the allocation
exports WebAssembly memory needs, is [the reactor ABI](plugin-reactor-abi.md); a
reactor guest and a Go host (`wasm.OpenReactor` in `pkg/plugin-wasm`) pass the
cross-language conformance suite.

## Decision

**The phone's own web engine is the first route. WAMR is the fallback.** This was
decided by the project owner after the evidence above.

1. **Primary: the system web engine** (WKWebView on iOS, the System WebView on
   Android). It ships nothing extra, and the spike shows the hard part works. A
   host loads the guest into a hidden webview and passes `ext.plugin/v1` messages
   across the page boundary. Its isolation is `restricted`.
2. **Fallback: WAMR** as an in-process interpreter in `pkg/plugin-engine`, for hosts
   where the webview is unsuitable: no usable webview, a need for the stricter
   `sandboxed` guarantee, or behaviour that must not depend on the platform's
   browser. Prototype it with the build options noted above (instruction metering
   and the thread manager), pin its version, and keep wasmi as the alternative if
   startup time, memory or deterministic fuel matter more than avoiding a Rust
   build. Do not pick wasm3 alone because of its maintenance notice.
3. **One guest format for both: a WebAssembly reactor.** The guest exports the
   existing `ext_plugin_*` functions plus an allocation export, so the same module
   runs in a webview and in an interpreter, and neither needs a blocking input
   queue, a Worker, a `SharedArrayBuffer` or cross-origin isolation. The allocation
   convention (`ext_plugin_alloc` and `ext_plugin_free`) is defined in
   [the reactor ABI](plugin-reactor-abi.md).
4. **Keep the command-style stdio guest** as a desktop and server format. It works in
   browsers only with the isolation machinery in the spike, so it is not the mobile
   format.

What this means in practice: the first mobile deliverable is a Swift webview host
and a Kotlin webview host that implement the plugin backend over the web engine's
message channel. The WAMR backend comes after, behind the same plugin contract, so a
host can choose either without changing its plugins.

## Since this was decided

Reported by the mobile SDK session, not re-run here:

- **The webview route works in a real WKWebView**, on macOS and in the iOS 18.3
  Simulator, using the web engine's unchanged `shim.js` and the
  [page API](web-engine.md#the-page-api-and-the-host-channel). A reactor built from
  the Rust SDK's conformance guest passed 9 of 9 tests there: handshake, Unicode and
  large-number echo, public and sanitized private errors, ordering, a guest that
  honours its deadline, and page destruction when a call overruns.
- **Starting a webview is slow at first.** In the simulator the first web view of a
  fresh app took about 32 seconds to start, then 11.7, then 3.1 as it warmed. A call
  timeout fired at about 1.0 second when asked for one. A host should start its
  webview early and keep it, not create one per call. Nothing was measured on a
  physical device, so treat these as simulator figures.

## Not verified

- Real iOS and Android devices; the store policies; in-app webview isolation.
- Android's System WebView, as a host or distinct from Chrome. The Android host is
  not built.
- wasmi and WAMR built for iOS and Android, and either running on a device. The WAMR
  fallback is not started.

The Swift and Java bindings belong to the mobile SDK work (`pkg/plugin-ios`,
`pkg/plugin-android`). This record covers only how a mobile host runs WebAssembly.
