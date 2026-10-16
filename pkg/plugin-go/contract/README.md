# Go plugin contract

`github.com/webong/ext/pkg/plugin-go/contract`: the ext plugin contract and the tooling
built on it, using only the standard library (Go 1.23). It holds the contract
data model, host sessions and guests, `ext.package/v1` manifests and the installed
package store's building blocks, JSON-line and bridge transports, process launching,
schemas and typed authoring, instances and streams, and the conformance suite.

Two packages sit at the top level because they belong to no single side: `packagekit` (the
package manifest and dependency model) and `plugintest` (the conformance suite). The rest are
four groups:

- `transport`: how frames travel. `transport/jsonline` (newline-delimited JSON over pipes),
  `transport/inprocess` (no IPC) and `transport/bridge` (relaying one transport to another).
- `validate`: how values are checked. `validate/schema` (the contract's bounded schema vocabulary)
  and `validate/jsonschema` (an offline JSON Schema subset).
- `host`: everything that runs plugins on a machine. `host` opens an installed package as a running
  plugin; `host/store` installs, verifies and lists packages under a root you choose;
  `host/process` starts the child process; `host/adapter` manages host-side adapters;
  `host/interop` resolves which backend serves a request; `host/nativego` loads a Go plugin built
  as a shared library.
- `guest`: what a plugin implementation uses. `guest/author` (typed operations and guest
  construction), `guest/capability` (the health and configuration contracts), `guest/instance`
  (configured resources) and `guest/stream` (the pull-stream service). The `Guest` type itself is
  in the root package, beside the host's `Session`.

`transport` and `validate` are leaves; `host` and `guest` build on them and never import each other.
The [`cengine`](../cengine) module has engine-routed variants of `host` and `host/store` with an
identical API.

This is the lean choice. Add [`cengine`](../cengine) for the C engine binding,
[`wasm`](../wasm) for WebAssembly or [`hashicorp`](../hashicorp) for go-plugin, or
require the umbrella module `github.com/webong/ext/pkg/plugin-go` to take them all.
A backend never depends on `cengine`.
