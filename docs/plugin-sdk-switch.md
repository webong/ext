# The SDK switch: scope and options

Status: a scoping note, written against `main` at `846af9b9`. It decides nothing; the
choices it needs are at the end. The parity checklist
([plugin-engine-parity.md](plugin-engine-parity.md)) says the common Go implementation is not
retired, and the independent language SDKs are not replaced, until the acceptance
requirements are met. This note says what "replaced" would mean for each SDK, what the C
engine can serve a guest today, and what each choice costs.

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
- **Zig.** `build.zig` can compile C itself, so this is the cheapest. The Zig package published
  from `dist/ext-zig` (PR #82) would have to carry the C sources it builds.
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

## Suggested order

1. Rust: most duplicated code, the engine binding is the most complete, and `guest-only` and
   WASI builds already exist.
2. Zig: `build.zig` compiles C, so packaging is straightforward.
3. TypeScript: after the delivery question (addon or WebAssembly) is decided.
4. Option B, only if the duplicated frame code keeps drifting.
5. Python: only if the standard-library-only promise is dropped.

Each step is its own change with its own evidence, in the order above.

## What is needed from the owner

1. **A, B or C** for Rust, Zig and TypeScript.
2. Whether `ext-io` (Rust, version 0.1.0) and the Zig package may start requiring a C
   toolchain, or must stay buildable from their language's tools alone.
3. For TypeScript, whether the portable no-import guest can go, or must be kept.
4. Whether Python stays standard-library only.
