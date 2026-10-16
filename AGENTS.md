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
`examples/` is its own module. Nothing may import the product modules `github.com/webong/ext/ctx` or
`github.com/webong/ext/ctn`; their directories are `src/ctx` and `src/ctn`. The module paths deliberately omit
`src/`, so the products are not `go get`-resolvable from the repository until their
paths and directories match; they are built from source and released as binaries.

`res/` is a container for reusable modules rather than a package. Each
subdirectory is its own Go module, and nothing lives at that level: no Go package
and no repository-wide policy. Keep container directories free of shared logic
and keep repo-wide tests in `src/ctx/internal/arch`. `res/web` and `res/credential`
depend only on the standard library; `res/graph` depends on `golang.org/x/sys`.

`lib/` is a container for native libraries that several language bindings
share. Nothing lives at that level. `lib/plugin` is the portable C plugin
engine, built with CMake: it is not a Go module, has no `go.mod` and is not
listed in `go.work`. The language bindings under `pkg/plugin-*` build on it.
Until it reaches parity, the pure-Go implementation in `pkg/plugin-go` is the
behavioral baseline; see `docs/plugin-engine-parity.md`. The shared conformance
fixtures live in `lib/plugin/testdata`. A change to the plugin contract or the
package manifest lands in both implementations, and the conformance checks
compare the two.

Each reusable library under `pkg/` is its own Go module, not a package inside
one repository-wide module. The root `go.work` keeps the repository building as
a single workspace while every module stays independently buildable. Keep each
library's dependencies pointing inward so it never depends back on its consumers;
`src/ctx/internal/arch` enforces that direction.

Runtime backends that own a heavy runtime are separate modules:
`pkg/plugin-go/hashicorp` (go-plugin, grpc) and `pkg/plugin-go/wasm` (wazero). Keep a
backend's non-test code dependent only on `pkg/plugin-go/contract` and `pkg/plugin-go/contract/jsonline`.
Tests that need a backend belong in that backend's module, not in the core
contract module, so the core stays free of the backend's runtime.

When adding a library module, list it in `go.work`, give it a `go.mod`, and add
matching `require` and `replace` directives to `src/ctx/go.mod`, plus `examples/go.mod`
when examples use it. Do not add
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
- `res/graph` for host, system, and process discovery, storage and transactions.
- `pkg/plugin-go` for the Go plugin library: the contract, sessions, guests, packages and runtime backends, on the C engine or in pure Go.
- `res/graph/supervisor` (inside the graph module) for process supervision and recovery.

Plugin runtime backends, including HashiCorp go-plugin, belong inside the
plugin library (for example `pkg/plugin-go/hashicorp`), together with their
dependencies. They are not CTX product adapters. Keep the shared host/guest
contract independent of backend choice so existing and future plugin
implementations can use it.

Product adapter engines and stores that are reused across adapters
(`adapters/chromium` module, engine code in `adapters/chromium/engine`, `adapters/firefox/engine`, `adapters/safari/native`,
`adapters/keychain/store`, `adapters/credman/store`, and
`adapters/secret_service/store`) are their own Go modules, listed in `go.work`
and resolved by `src/ctx/go.mod` while unpublished. Product adapter
executables use the same plugin library. `pkg/plugin-go/contract` owns the
adapter process protocol and adapter descriptor helpers. Adapters and other Go
programs import `pkg/plugin-go`, the one Go entry point: it re-exports the
`contract` data model and serves sessions, guests, digests and routes on the C
engine (`ext_cengine` and cgo) or in pure Go, so `CGO_ENABLED=0` builds keep
working. There is no separate `pkg/plugin` module: the Go plugin library is the
Go language package of the plugin engine, beside the other `pkg/plugin-*`
language packages. Its `packages`, `store` and `host` subpackages cover manifests, the
installed package store and opening a package; the root stays free of
`packagekit` so it does not reach `res/graph`. Runtime backends
(`pkg/plugin-go/hashicorp`, `pkg/plugin-go/wasm`) and the C ABI loader in `pkg/plugin-go/cengine/cshared` implement
`contract.Backend` and import `pkg/plugin-go/contract`. Hosts import `pkg/plugin-go/contract/adapter`,
which owns manifests, the installed store, trust and invocation; a product keeps
only its own selection and commands.

Language SDKs and bindings belong under `pkg/<language>/` (Go, Rust, Zig and
TypeScript), with language-specific embedding examples alongside them. The
mobile SDKs are named for their platforms because they are native to them:
`pkg/plugin-ios` (Swift, with Objective-C through the C headers) and
`pkg/plugin-android` (Java, with Kotlin through the same API). Keep
`pkg/plugin-go` focused on the contract, the engine API and runtime backends. The Go-to-C guest binding belongs in
`pkg/plugin-go/cengine/cshared/guest`. The language-neutral C ABI header
(`lib/plugin/include/ext_plugin.h`) belongs to the engine; its constants and wire
envelope (`cshared/abi`) and the runtime loader (`cshared`) live under
`pkg/plugin-go/cengine/cshared`; there is no separate `pkg/plugin-go/cengine/cshared` module. `pkg/plugin-go/wasm` stays its own module because guests
in other languages run through it.

Moving a library into or out of `pkg/` changes public import paths. Update
`AGENTS.md`, the docs, and the language SDKs together, and verify with
`go build ./...` and `go vet ./...`.
