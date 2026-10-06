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

`pkg/` is the home for reusable libraries. CTX adapters, Xallet, Cymonkey, and
other ecosystem consumers build on them; `internal/` stays private to CTX and
must not be imported by consumers.

`res/` is a container for reusable modules rather than a package. Each
subdirectory is its own Go module, and nothing lives at that level: no Go package
and no repository-wide policy. Keep container directories free of shared logic
and keep repo-wide tests in `internal/arch`. `res/browser` and `res/credential`
depend only on the standard library.

Each reusable library under `pkg/` is its own Go module, not a package inside
one repository-wide module. The root `go.work` keeps the repository building as
a single workspace while every module stays independently buildable. Keep each
library's dependencies pointing inward so it never depends back on its consumers;
`internal/arch` enforces that direction.

Runtime backends that own a heavy runtime are separate modules:
`pkg/plugin-hashicorp` (go-plugin, grpc) and `pkg/plugin-wasm` (wazero). Keep a
backend's non-test code dependent only on `pkg/plugin` and `pkg/plugin/jsonline`.
Tests that need a backend belong in that backend's module, not in the core
contract module, so the core stays free of the backend's runtime.

When adding a library module, list it in `go.work`, give it a `go.mod`, and add
matching `require` and `replace` directives to the root `go.mod`. Do not add
`replace` directives to library modules: Go ignores them for consumers, so they
only misrepresent what a published module resolves. `GOWORK=off go build` in a
library module is expected to fail until its dependencies are published.

Test each module in workspace mode, and note that `go test ./...` only covers the
root module. Versioning follows [module versioning](docs/module-versioning.md):
one shared `MAJOR` across all modules, independent `MINOR` and `PATCH`, and tags
named for each module path.

The reusable libraries are:

- `res/browser` for portable browser contracts and workflows.
- `res/credential` for portable credential serving and client code.
- `pkg/graph` for host, system, and process discovery.
- `pkg/plugin` for the shared host/guest contract and runtime backends.
- `pkg/graph/supervisor` (inside the graph module) for process supervision and recovery.

Plugin runtime backends, including HashiCorp go-plugin, belong inside the
plugin library (for example `pkg/plugin-hashicorp`), together with their
dependencies. They are not CTX product adapters. Keep the shared host/guest
contract independent of backend choice so existing and future plugin
implementations can use it.

Product adapter engines and stores that are reused across adapters
(`adapters/chromium` module, engine code in `adapters/chromium/engine`, `adapters/firefox/engine`, `adapters/safari/native`,
`adapters/keychain/store`, `adapters/credman/store`, and
`adapters/secret_service/store`) are their own Go modules, listed in `go.work`
and resolved by the root `go.mod` while unpublished. Product adapter
executables use the same plugin library. `pkg/plugin` owns the
adapter process protocol and adapter descriptor helpers. Adapters import
`pkg/plugin` directly to build guests, while CTX `internal/` imports it to
build hosts.

Language SDKs and bindings belong under `pkg/<language>/` (Go, Rust, Zig and
TypeScript), with language-specific embedding examples alongside them. Keep
`pkg/plugin` focused on engine contracts, runtime backends and shared
conformance fixtures. The Go-to-C guest binding belongs in
`pkg/plugin-go/cshared/guest`; the language-neutral C ABI header and runtime loader
remain in `pkg/plugin-cshared`.

Moving a library into or out of `pkg/` changes public import paths. Update
`AGENTS.md`, the docs, and the language SDKs together, and verify with
`go build ./...` and `go vet ./...`.
