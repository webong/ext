# Adapter ownership

Anything specific to a product, provider, browser, or native tool belongs in its
adapter package under `adapters/<name>/`. This includes native protocols,
installation and activation, executable and profile discovery conventions,
storage formats, signing tools, store URLs, settings paths, and diagnostics.

CTX core owns generic contracts, validation, trust, selection, and dispatch.
Shared libraries may implement portable workflows and utilities, but must not
choose native behavior by adapter name or supply a browser-specific fallback.
Adapters provide that behavior through declared capabilities and backends.

Related adapters may reuse an engine from the adapter that owns the native
mechanism, such as `adapters/chromium/engine`. Each product adapter supplies its
own paths, identifiers, capabilities, and policy through configuration.

Apply this boundary when bringing code into CTX: move provider-specific behavior
into the owning adapter before wiring it into shared workflows.

# Public libraries

`pkg/` is the home for reusable libraries. CTX adapters and other
ecosystem consumers build on them; `internal/` stays private to CTX and
must not be imported by consumers.

The repository root is only the workspace: it holds `go.work` and no Go module or
package. Binary releases use product tags, `ctx-vX.Y.Z` and `ctn-vX.Y.Z`; those are not Go
module versions. Bare `vX.Y.Z` tags are not used.
`src/` is a container like `res/`; the context manager is the module
`src/ctx` (`src/ctx/cmd/ctx`, with its private code in `src/ctx/internal`) and
the content manager is `src/ctn`. This repository is ext: it provides libraries
and binaries, and ctx and ctn are binaries built on those libraries. A product
never imports another product; shared code goes in `pkg/` or `res/`.
`examples/` is not a Go module: it holds only samples that need no Go build (a shell adapter and an Android
consumer app). Go examples live in the library they show, as an `examples/` directory inside that module:
`pkg/plugin-go/examples` for plugins (the umbrella requires every backend), `res/system/examples` and
`res/web/examples` for those libraries. Nothing but `cmd/ext` may import the product modules `github.com/webong/ext/ctx` or
`github.com/webong/ext/ctn`, through their root packages (`ctx.Run`, `ctn.Run`; see [the ext command](docs/adr-ext-command.md)); their directories are `src/ctx` and `src/ctn`. The module paths deliberately omit
`src/`, so the products are not `go get`-resolvable from the repository until their
paths and directories match; they are built from source and released as binaries.

`res/` is a container for reusable modules rather than a package. Each
subdirectory is its own Go module, and nothing lives at that level: no Go package
and no repository-wide policy. Keep container directories free of shared logic
and keep repo-wide tests in `src/ctx/internal/arch`. `res/web` and `res/credential`
depend only on the standard library; `res/system` depends on `golang.org/x/sys`.

`lib/` is a container for native libraries that several language bindings
share. Nothing lives at that level. `lib/plugin` is the portable C plugin
engine, built with CMake: it is not a Go module, has no `go.mod` and is not
listed in `go.work`. The language bindings under `pkg/plugin-*` build on it.
Until it reaches parity, the pure-Go implementation in `pkg/plugin-go/contract`
(and the pure-Go build of `pkg/plugin-go/cgoengine`) is the behavioral baseline; see `docs/plugin-engine-parity.md`. The shared conformance
fixtures live in `lib/plugin/testdata`. A change to the plugin contract or the
package manifest lands in both implementations, and the conformance checks
compare the two.

Each reusable library under `pkg/` is its own Go module, not a package inside
one repository-wide module. The root `go.work` keeps the repository building as
a single workspace while every module stays independently buildable. Keep each
library's dependencies pointing inward so it never depends back on its consumers;
`src/ctx/internal/arch` enforces that direction.

`pkg/plugin-go/` is a tree of modules, not one module, so an application takes
only the parts it needs: `contract` (standard library only), `cgoengine` (the C
engine binding, with a pure-Go build of the same API), `wasm` (wazero) and
`hashicorp` (go-plugin, grpc), plus `pkg/plugin-go` itself as a thin umbrella
module that requires them all at matching versions. Nested modules are excluded
from the module that contains them, so an import path must belong to exactly one
module and never move between modules at a version a consumer may still resolve;
published paths are not reused by a different module.

Runtime backends that own a heavy runtime are separate modules:
`pkg/plugin-go/hashicorp` (go-plugin, grpc) and `pkg/plugin-go/wasm` (wazero).
A backend depends on `pkg/plugin-go/contract` only, in code, tests and `go.mod`,
and never on `pkg/plugin-go/cgoengine`, so a wazero or grpc user needs no C
toolchain and the engine binding is not in their module graph. Tests that need
a backend belong in that backend's module, and tests or commands that need both
a backend and the engine belong in the umbrella module, not in `contract`, so
the core stays free of every runtime. `pkg/plugin-go/contract` uses the standard
library only. `src/ctx/internal/arch` enforces both rules.

When adding a library module, list it in `go.work`, give it a `go.mod`, and add
matching `require` and `replace` directives to `src/ctx/go.mod` only if ctx imports it
(adapters are loaded as ext packages, so `src/ctx/go.mod` never lists them). Do not add
`replace` directives to library modules: Go ignores them for consumers, so they
only misrepresent what a published module resolves. `GOWORK=off go build` in a
library module is expected to fail until its dependencies are published.

Test each module in workspace mode, and note that `go test ./...` only covers the
module in the current directory. Versioning follows [module versioning](docs/module-versioning.md):
one shared `MAJOR` across all modules, independent `MINOR` and `PATCH`, and tags
named for each module path.

The reusable libraries are:

- `res/web` for portable web contracts and workflows: browsers, and the web engine that runs web content through them.
- `res/credential` for portable credential serving and client code.
- `lib/plugin` for the portable C plugin engine shared by the `pkg/plugin-*` language bindings.
- `res/system` for observing the machine: host, shell, file system and web view discovery and process inspection, with `res/system/graph` (the namespaced graph store and its transactions) and `res/system/supervisor` (process supervision and recovery).
- `pkg/plugin-go/contract` for the Go plugin contract, sessions, guests, packages, bridges and tooling (standard library only).
- `pkg/plugin-go/c2goengine` for the C engine's portable core translated to Go by ccgo (no cgo, no C compiler). The translation is generated per platform by its `tools` module and held against the pure-Go contract, the oracle, by `contract/plugintest/enginetest`; it depends on `contract` only.
- `pkg/plugin-go/cgoengine` for the Go binding of the C engine, with a pure-Go build of the same API and the C ABI loader.
- `pkg/plugin-go/wasm` and `pkg/plugin-go/hashicorp` for the WebAssembly and HashiCorp go-plugin backends.
- `pkg/plugin-go` for the umbrella module that requires all of the above and holds code that needs several of them, such as `pkg/plugin-go/core`, which chooses between the pure-Go contract, `cgoengine` and `c2goengine` (the only place that knows all three).

Plugin runtime backends, including HashiCorp go-plugin, belong inside the
plugin library (for example `pkg/plugin-go/hashicorp`), together with their
dependencies. They are not CTX product adapters. Keep the shared host/guest
contract independent of backend choice so existing and future plugin
implementations can use it.

Product adapter engines and stores that are reused across adapters
(`adapters/chromium` module, engine code in `adapters/chromium/engine`, `adapters/firefox/engine`, `adapters/safari/native`,
`adapters/keychain/store`, `adapters/credman/store`, and
`adapters/secret_service/store`) are their own Go modules, listed in `go.work`.
ctx does not import them: it loads adapters as installed ext packages. Product adapter
executables use the same plugin library. `pkg/plugin-go/contract` owns the
adapter process protocol and adapter descriptor helpers (`contract/host/adapter`), and holds the pure-Go package store and host (`contract/host`, `contract/host/store`). Adapters and other Go
programs import `pkg/plugin-go/cgoengine`, the Go entry point to the engine: it
re-exports the `contract` data model and serves sessions, guests, digests and
routes on the C engine (`ext_cengine` and cgo) or in pure Go, so `CGO_ENABLED=0`
builds keep working. There is no separate `pkg/plugin` module: its code is
`pkg/plugin-go/contract`. Everything that runs plugins on a machine is grouped under `contract/host`: `host` opens an
installed package, and its subpackages `store` (the installed package store), `process` (starting the
child), `adapter` (the host-side adapter manager), `interop` (route resolution) and `nativego`
(loading a Go plugin built as a shared library). What a plugin implementation uses is grouped under
`contract/guest`: `author`, `capability`, `instance` and `stream`; the `Guest` type stays in the root
package beside `Session`. How frames travel is grouped under `contract/transport` (`jsonline`, `inprocess`, `bridge`) and how values
are checked under `contract/validate` (`schema`, `jsonschema`). The groups are layered: `transport` and
`validate` import neither `host` nor `guest` nor each other, and `host` and `guest` never import each
other (`src/ctx/internal/arch` enforces it). `package` (declared name `packagekit`, since `package` is a Go keyword) and `plugintest` stay at
the top because both sides use them. `cgoengine/packages` covers manifests over the
engine, and `cgoengine/host` and `cgoengine/host/store` are engine-routed variants of `contract/host` and
`contract/host/store` with an identical exported API (a rule `src/ctx/internal/arch` enforces), so
swapping the import never needs a code change.
`packagekit` has no dependency on `res/system`; joining a package plan to the host
graph is `plugingo.PlanGraph` in the umbrella module. Runtime backends
(`pkg/plugin-go/hashicorp`, `pkg/plugin-go/wasm`) and the C ABI loader in
`pkg/plugin-go/cgoengine/cshared` implement `contract.Backend` and import
`pkg/plugin-go/contract`. Hosts import `pkg/plugin-go/contract/host/adapter`, which
owns manifests, the installed store, trust and invocation; a product keeps only
its own selection and commands.

A published Go module contains only its own directory. `pkg/plugin-go/cgoengine`
therefore carries the C sources it is built from as `_csrc`, a generated copy of
`lib/plugin` made by `scripts/cgoengine-sync.sh`; `TestCSourcesMatchCanonical`
fails when the copy drifts. `lib/plugin` stays the canonical home that the iOS,
Android, Rust and Zig bindings build from: edit it, run the script, commit both.

Language SDKs and bindings belong under `pkg/<language>/` (Go, Rust, Zig and
TypeScript), with language-specific embedding examples alongside them. The
mobile SDKs are named for their platforms because they are native to them:
`pkg/plugin-ios` (Swift, with Objective-C through the C headers) and
`pkg/plugin-android` (Java, with Kotlin through the same API). Keep
`pkg/plugin-go` focused on the contract, the engine API and runtime backends. The Go-to-C guest binding belongs in
`pkg/plugin-go/cgoengine/cshared/guest`. The language-neutral C ABI header
(`lib/plugin/include/ext_plugin.h`) belongs to the engine; its constants and wire
envelope (`cshared/abi`) and the runtime loader (`cshared`) live under
`pkg/plugin-go/cgoengine/cshared`, inside the `cgoengine` module. `pkg/plugin-go/wasm`
stays its own module because guests in other languages run through it.

Moving a library into or out of `pkg/` changes public import paths. Update
`AGENTS.md`, the docs, and the language SDKs together, and verify with
`go build ./...` and `go vet ./...`.
