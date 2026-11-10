# ADR: Layers, the rules of `res/`, and language tiers

Status: accepted on 2026-11-10, written against `main` at `71e1b8c0`. It collects decisions that
were made one at a time ([hosts in every language](adr-hosts-in-every-language.md),
[engine selection](adr-engine-selection.md), [web layout](adr-web-layout.md),
[graph runtime](adr-graph-runtime.md)) into one picture. It adds three things: the rules every
`res/` module follows, the plan for `res/graph` storage and the C store `lib/ledger`, and the
support tier of each language.

## Layers

Each layer depends only on the layers above it in this table. `AGENTS.md` and
`src/ctx/internal/arch` hold the exact boundaries.

| Layer | What it is | Language |
| --- | --- | --- |
| 1. Contracts | `ext.plugin/v1`, the package manifest, the `ext.system` vocabulary, and the shared fixtures (`lib/plugin/testdata`, `res/web/testdata`, and the fixtures `res/system` and `res/graph` will get) | Data |
| 2. Engines | `lib/plugin` (C, embeddable anywhere), `pkg/ext-go/engine` (the Go reference), `c2goengine`, `cgoengine`, and later `lib/ledger` | C where it must embed anywhere; Go as the reference |
| 3. OS mechanisms | `res/system`, `res/web`, `res/int` | Go on desktop and server; each OS's own language inside an app |
| 4. Read model | `res/graph`: the store, the recorder of `ext.system`, the supervisor | Go |
| 5. SDKs | `pkg/ext-*`: guests and hosts over the protocol; `ext-ios` and `ext-android` also bundle the native `res/` code | Every language |
| 6. Adapters | `adapters/*`: one product, provider, browser or native tool each, loaded as packages | Mostly Go |
| 7. Products | `ctx`, `ctn`, `ext`; Cymonkey and Xallet outside this repository | Selection, commands, policy |

The layers rest on three principles:

- **A contract is data.** What a library means is written down as versioned fixtures, not as the
  behaviour of one implementation. Go is the reference implementation, but where Go and a fixture
  disagree, the fixture decides and the fix lands in Go.
- **Native code goes where the OS is.** The host side is Go. Code that calls an OS framework, or that
  lives inside an app, is written in that OS's own language: Swift on Apple platforms, Java or Kotlin
  on Android, C# on Windows. Go is a poor guest inside another runtime: it brings its whole runtime,
  only one can live in a process, it takes over signals and it cannot be unloaded. The one component
  that must embed anywhere, the plugin engine, is C.
- **Other languages cross the protocol.** A language reaches what another language implements
  through `ext.plugin/v1`. It does not get a C ABI per library or a second implementation.

## The rules of `res/`

`res/` owns mechanisms. Authorization, approval UI, remote access, routing and policy belong to
consumers, as `AGENTS.md` already says. Every `res/` module also follows five rules. The first
three apply to new code at once and to existing packages as they are next touched. Rule 5 is a
design obligation.

1. **Fixtures are the contract.** Behaviour meant to be identical in Go, Swift, Java and C# (data
   shapes, encodings, parsing, validation, normalisation and error codes) is covered by versioned
   fixtures under the module's `testdata/<area>/v<N>`. Every implementation runs them, as the
   cookie, userscript and bookmarklet rules already do (`scripts/web-differential.sh`). Native
   observation that cannot be reproduced on a runner is tested by each language. It is not forced
   into a fixture.
2. **Report capability, never branch on an OS name.** Each package can say what this platform
   supports and why something is missing. The reasons are fixed: unsupported on this platform,
   permission not granted, no device, or the native tool is not installed. A consumer asks once and
   never writes `if ios`. `res/system/capture/surface.DesktopDriver` is the model. A missing native
   tool is reported by the adapter that owns the tool.
3. **One vocabulary of error codes.** Errors that cross a language boundary carry a stable code. The
   codes start from the engine's statuses (`ext_status` in `lib/plugin/include/ext_host.h`) and
   remote error codes, in lowercase: `invalid`, `denied`, `unsupported`, `not_found`, `exists`,
   `timeout`, `canceled`, `expired`, `busy` and `too_large`. A module adds a code only through a
   fixture. `res/system/testdata/codes.json` lists the codes the fixtures use, and adds three:
   `changed` (a compare-and-swap failed because the generation or state moved on, and the caller
   retries), `unavailable` (supported here but not usable now; the capability reason says why) and
   `io` (a native call failed; the engine's `EXT_IO`).
   Error text is never part of a contract and never carries a value it was given.
4. **Bounded and cancellable.** Every operation that reads, waits or buffers has a stated bound on
   size, count and time, and can be cancelled. No package keeps hidden global state.
5. **Served over the protocol.** Rust, Python, TypeScript and the other SDKs reach `res/` through
   a Go host serving it over `ext.plugin/v1`, with a thin typed wrapper in the SDK for the calls a
   consumer needs. The service is designed before the first consumer asks, and built when one
   does. Until then the `ext-io` names `system`, `web` and `int` stay reserved
   ([module versioning](module-versioning.md#the-ext-io-language-packages)).

## `res/graph` storage and `lib/ledger`

The [graph runtime](adr-graph-runtime.md) says large installations need indexed persistence behind
the same interface. That work comes in three steps:

1. **The store contract.** Vertices, edges, provenance, commits, changes, the naming and attribute
   rules, the secret-marker check, idempotency, history compaction and the error codes become
   fixtures under `res/graph/testdata/store/v1`. Schemas become declarative rules instead of the Go
   callback `Schema.Validate`, so a schema can be checked in any language and stored with the graph.
2. **SQLite storage in Go.** The JSON snapshot that is rewritten on every commit is replaced by
   SQLite tables for vertices, edges, changes, schemas and idempotency records. The layout of the
   tables is part of the contract. The Go store uses `modernc.org/sqlite`, so `res/graph` still needs
   no cgo. An existing graph file is not migrated: the new store starts empty and the recorder
   rebuilds what it observes. This replaces the snapshot's lock-and-reread scheme with SQLite's own
   locking for short-lived commands and long-lived consumers. Watches poll the durable cursor as they
   do now. The recorder and the supervisor stay in Go.
3. **`lib/ledger`, only on demand.** A C store under `lib/ledger` is built when a program that cannot
   embed Go must write a graph in its own process, a mobile app for example. It is a thin layer that
   applies the contract over SQLite and is held to the same fixtures. It follows the pattern of
   `lib/plugin`: CMake, no Go code in its tree, and Go as the reference. A program that only reads
   the graph needs no library, because the documented tables can be read with any SQLite.

## Language tiers

The project carries more languages than one owner can hold to the same standard everywhere. A tier
sets what a change must keep working, and what a missing platform means.

| Tier | Languages | A change must keep | Published |
| --- | --- | --- | --- |
| 1 | Go, C (`lib/plugin`), Swift, Java/Kotlin, C# | Every check passing on every platform the language ships for. A gap is a bug. | Go proxy, SwiftPM, GitHub (AAR), NuGet |
| 2 | Rust, TypeScript, Python, Zig | The shared fixtures and host cases on Linux and macOS, and on Windows where the toolchain runs on a hosted runner. A Windows gap is tracked. | crates.io, npm, PyPI, `dist/ext-zig` |
| 3 | Ruby, PHP, Lua | The shared fixtures and host cases on Linux and macOS. Windows is best effort, and a gap is documented, not fixed by default. | Not published until a consumer asks |

Tier 1 is the platform base that Cymonkey and Xallet build on. Tier 2 holds the published `ext-io`
packages. Tier 3 proves the protocol is language-neutral. Its SDKs are complete, and its
guarantees are narrower. A language moves up a tier when a consumer depends on it.

## Consequences

- Fixtures for `res/system` and tests for `Ext.Audio.Windows` and `Ext.Clipboard.Windows` are the
  first work under rules 1 and 4. Each later `res/` change brings its package in line with rules 2
  and 3.
- The `res/graph` store contract and SQLite backend are the next graph work. `lib/ledger` waits for an
  embedder.
- The Ruby and Lua hosts on Windows (#119), and the PHP SDK and host on Windows, which are in progress,
  go beyond what tier 3 requires. They are done because the gaps were small. They are not a precedent
  that tier 3 must run everywhere.
