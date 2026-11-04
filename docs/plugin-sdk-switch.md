# The SDK switch: scope and options

Status: decided on 2026-11-04 (see [Decision](#decision)); written against `main` at
`846af9b9`. The parity checklist ([plugin-engine-parity.md](plugin-engine-parity.md)) says the
common Go implementation is not retired, and the independent language SDKs are not replaced,
until the acceptance requirements are met. This note says what "replaced" means for each SDK,
what the C engine can serve a guest today, what each choice costs, and which choice was made.

## Where each SDK stands

Line counts are the non-test source of the guest and host SDK at this commit.

| SDK | Guest today | Host today | Uses the C engine |
| --- | --- | --- | --- |
| Go | `engine/guest/*`, the oracle | `engine` sessions | Yes: `pkg/ext-go/core` chooses between the oracle, `cgoengine` and `c2goengine` ([ADR](adr-engine-selection.md)). The switch is done; the oracle stays. |
| Swift (`pkg/ext-ios`) | `ExtPluginGuest`: `Guest.swift` calls `ext_guest_create`, `ext_guest_invoke` and `ext_host_validate_json`. The wire types (`Wire`), JSON lines (`JSONLine`, `Connection`), frames (`Frames`) and routing are Swift (about 1,600 lines beyond `Guest.swift`). | WebView and WAMR hosts, store through the engine | Dispatch and JSON validation only |
| Java (`pkg/ext-android`) | `Guest.java` calls `NativeEngine.create/invoke/descriptor/validateJson`. `JsonLine`, `Frames`, `Connection`, `Reporter` and the rest of the wire code are Java (about 1,500 lines with `Guest.java`). | WAMR host, store through the engine | Dispatch and JSON validation only |
| Rust (`ext-io`, `pkg/ext-rust/src`) | `wire.rs` (587), `serve.rs` (555), `frames.rs` (376), `guest.rs` (320), `capability.rs` (163): about 2,000 lines of its own | `host.rs`, `native.rs`, `cabi.rs` (about 590) | No. `ext-io` does not depend on the engine crate (`pkg/ext-rust/engine`), which is a separate, additive crate |
| Zig (`pkg/ext-zig/src`) | `wire.zig` (368), `frames.zig` (318), `author.zig` (225), `capability.zig` (172), `jsonline.zig` (126) | `host.zig`, `cabi.zig` | No. `build.zig` does not reference the engine; `pkg/ext-zig/engine` is a separate, additive package |
| TypeScript (`pkg/ext-ts`) | `index.mjs` (1,312 lines, no imports, so it runs anywhere JavaScript does) | the same file, plus `node.mjs` | No. The Node addon is the separate export `./plugin/engine` |
| Python (`pkg/ext-python`, `ext_io`) | `frames.py` (446), `guest.py` (412), `_core.py` (164), `capability.py` (156): standard library only, `requires-python >= 3.8` | none (guest only) | No, and it has no native dependency to build on |

So the switch is a real question for four SDKs, Rust, Zig, TypeScript and Python, and a
smaller one for Swift and Java, which already delegate the part that is easiest to share.

## What the C engine can serve a guest today

- **Dispatch (`ext_guest_*`)**: an immutable descriptor, request validation against it,
  deadlines, owned buffers, error sanitization, and the `invalid_request` answer. Bound in Go,
  Rust, Zig, Node, Swift and Java.
- **Services by name (`ext_engine_call`)**: `descriptor.validate/match/select/requirements`,
  `request.validate`, `response.validate`, `frame.validate`, `schema.validate/check`,
  `endpoint.check/validate`, `protocol.negotiate`, `route.resolve`. These are the rules a guest
  SDK re-implements in `wire.*` today.
- **Packages and the store**: bound everywhere a store is wanted, including discovery.

What it does not have is the **guest side of a connection**: a loop that reads JSON lines from
standard input, the sender-side progress token bucket, and the reverse-call client (the `g<N>`
ids, the `parent` and deadline rules, the reader). The C engine implements the host half of
progress and reverse calls (`progress.c`, `reverse.c`). The guest half exists once per
language, six times, held to Go by the shared fixtures (`model-cases.json`, `frame-cases.json`,
`progress-limiter-cases.json`, `endpoint-cases.json`) and by the cross-language frames suite
([plugin-guest-frames.md](plugin-guest-frames.md)).

## Options

**A. Delegate validation and dispatch (the Swift and Java shape).** Rust, Zig and TypeScript
call the engine for the `wire.*` rules and for dispatch, through bindings that already exist
(`Guest` in the Rust and Zig engine packages, `guest` in the Node addon). The serve loop,
frames and capability helpers stay in the language. This removes the rules that have to change
whenever the contract does. It does not touch the frame code, which is where the six copies
are.

**B. A, plus frame state machines in C.** Add guest-side pieces with no I/O: the sender limiter,
the reverse id and parent rules, and the frame checks as `ext_guest_frames_*`. Each language
keeps its own reader and threading model, since they differ on purpose (a reader thread in
Rust, Java and Swift, with Rust reading inline as a WASI command must; the waiting thread
reads inline in Python and Zig; JavaScript keeps reading while a handler runs). This is an ABI addition and a real design task. It is where the largest
duplication goes away, and where an engine bug would reach every SDK at once.

**C. Keep the independent implementations.** The fixtures and the cross-language suites stay
the guarantee, as they are now. A contract change is made in `lib/plugin`, in the oracle, and
in up to six SDKs, and each can drift until a fixture catches it.

Python is a separate decision under any option. Switching it means shipping a native library
with `ctypes` (platform wheels) and dropping the standard-library-only promise. Staying pure
Python is consistent with how the parity doc already describes it (the guest reads only the
package's `descriptor` key on purpose).

## What a switch costs the user of the SDK

The code is the smaller part. The price is what an application needs to build or ship:

- **Rust.** `ext-io` today needs only crates (`serde`, `serde_json`, `time`, `libloading`) and
  builds for WASI. The engine crate needs the C engine's static library
  (`EXT_ENGINE_LIB_DIR`), and has a `guest-only` feature and a `wasm32-wasip1` build. Folding it
  in means either vendoring the C sources and compiling them from `build.rs` (a C toolchain for
  every user of `ext-io`) or shipping prebuilt libraries. Not tried.
- **Zig.** `build.zig` can compile C itself and cross-compile it, WASI included, so this is the
  cheapest. Today the engine package (`pkg/ext-zig/engine`) links a library built beforehand with
  CMake (`-Dengine-lib=`), and the SDK (`pkg/ext-zig`) does not use the engine at all. The Zig
  package published from `dist/ext-zig` (PR #82) would have to carry the C sources it builds.
- **TypeScript.** Today there is nothing to build or install. The engine reaches JavaScript as a
  Node addon (a prebuilt binary per platform and Node version) or, as a candidate worth testing
  and not yet tried, the guest core compiled to WebAssembly, which would stay portable. Either
  breaks "no imports".
- **Python.** See above.

A second cost is behavior. TypeScript's fixture run lists exceptions that follow from JavaScript
numbers (the engine keeps exact number text). Delegating the parse to C would remove those
exceptions, which is a fix and a behavior change to release-note.

## Acceptance for one SDK

While ext is in development an SDK switches in one change and its old implementation is
deleted: no flag, no second path, no staged loader. Before that change:

1. Every shared fixture passes with no new exception, and the listed ones do not grow.
2. The cross-language suites that include the SDK (`TestFramesGuests`, `TestCapabilityGuests`,
   `TestCapabilityHosts`) pass unchanged.
3. A mutation check: change one rule in `lib/plugin`, and the SDK's test fails. That shows the
   SDK really runs the engine's rule.
4. It builds and runs on Linux, macOS and Windows, and for WASI where the SDK has a WASI
   guest today (Rust, Zig).
5. The dependency and build cost above is documented in the SDK's README and agreed.

## Decision

The owner asked for the best call to be made. This is it, with what would change it.

1. **The end state is A** for Rust, Zig and TypeScript: validation and dispatch come from the C
   engine, and the serve loop, frames and typed authoring stay in the language. The parity
   checklist already names the C engine as the base with the pure-Go engine as the oracle; Swift
   and Java already work this way; and it removes the rules that must change whenever the contract
   does. **C is the state until a step lands, not a destination.**
2. **B waits for evidence.** The parity doc records no case of a frame copy drifting, and B is an
   ABI addition whose bug would reach every SDK. Revisit it if a frame rule changes in more than
   two SDKs at once, or a fixture catches a divergence in one copy.
3. **Python stays independent.** The standard-library-only promise is kept; the shared fixtures
   and the frames suite are its guarantee.
4. **Each other SDK switches only after its build gate passes, in one change, with its old
   implementation deleted** (no flag, no second path):
   - **Zig first.** Gate: `build.zig` compiles the C guest sources itself (the list in
     `lib/plugin/CMakeLists.txt`, `core_sources`: ten portable files) for the host targets and
     `wasm32-wasi`, with no prebuilt library and no CMake. Smallest gate, and no new requirement
     for its users beyond Zig.
   - **Rust second.** Gate: `ext-io` builds with a generated copy of the C sources vendored in the
     crate (as `cgoengine` carries `_csrc`) and compiled from `build.rs`, on Linux, macOS, Windows
     and `wasm32-wasip1` in CI. If the WASI build needs more than a documented compiler setting,
     `ext-io` stays independent and the engine crate stays the C-backed path.
   - **TypeScript last.** Gate: the guest core compiled to WebAssembly passes the shared fixtures
     in Node with no native addon. The Node addon is rejected as the route: it would end the
     no-import portable guest and add a binary per platform and Node version. If the WebAssembly
     route fails, TypeScript stays independent.
5. **A step that fails its gate is recorded as that SDK's final state** (independent, held by the
   fixtures), not retried indefinitely.

## First step: Zig

1. `pkg/ext-zig/build.zig` compiles the ten guest sources with `addCSourceFiles` and exposes the
   engine services (`ext_engine_call`) to the SDK.
2. `wire.zig` calls them for `descriptor.validate`, `descriptor.match`, `request.validate` and
   `response.validate` in place of its own copies (`validate`, `match`, `validateRequest`); the
   typed decoding it also holds stays.
3. Acceptance as above: every shared fixture with no new exception, `TestFramesGuests`,
   `TestCapabilityGuests` and `TestCapabilityHosts` unchanged, a mutation in `lib/plugin` that
   fails the Zig tests, and the WASI guest still building.

### Status (2026-11-04, branch `engine/zig-switch`)

Done:

- **The gate passes.** `build.zig` compiles the ten sources itself and renames the vendored parser
  with `-D` flags, as CMake does (checked by comparing the generated names with CMake's). The core
  builds for `wasm32-wasi`, Linux (gnu x86_64, musl aarch64), Windows (x86_64, aarch64) and macOS.
  It does not build for iOS (Zig has no iOS sysroot) or `wasm32-freestanding` (no libc); neither is
  a Zig SDK target. The build works from the package and from the repository root
  (`--build-file pkg/ext-zig/build.zig`, which is how `scripts/plugin-crosslang.sh` calls it).
- **Descriptor and request validation come from the engine.** `wire.validate`, `wire.match` and
  `wire.validateRequest` call `descriptor.validate`, `descriptor.match` and `request.validate`;
  the Zig copies of those rules (about 70 lines) are deleted.
- **Evidence.** The 31 SDK tests, with the shared descriptor and request fixtures judged by the
  engine, pass. The cross-language suites pass against the Zig guest and host: 54 Zig subtests of
  `TestFramesGuests`, `TestCapabilityGuests`, `TestForeignGuests` (JSON lines, C ABI, WASI),
  `TestForeignMalformedFrames`, `TestForeignHosts` and `TestCapabilityHosts`.
- **Mutation checks.** Removing the plugin-identity rule from `lib/plugin/src/wire.c` fails the
  fixture test. Removing the surface rule did *not* fail anything, because no shared fixture
  declares a `surface`: the rule was untested in every language. `tests.zig` now holds a test for
  it, which fails under that mutation. The fixtures themselves still lack the case.

Known cost and what is left:

- Each request is validated by sending the cached descriptor and the request to the service:
  about 18 microseconds for a descriptor of 40 operations (release build, this machine), mostly
  re-parsing the descriptor. The engine's guest (`ext_guest_create` and `ext_guest_invoke`)
  snapshots the descriptor once; moving dispatch onto it is the next slice, and removes both the
  cost and the context, deadline and error-sanitizing code the SDK keeps for itself.
- Still Zig copies: `identifier` and `validateIdentity` (the frame ids and the registry identity),
  the strict decoders that build typed values from JSON, and the frame limiter.
- `dist/ext-zig` (PR #82) must carry the C sources, since a published package contains only its
  own directory; `-Dengine-src` points the build at them.

Building it locally needs Zig 0.17.0, pinned with its checksum in `plugin-sdk.yml`.
