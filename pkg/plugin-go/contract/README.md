# Go plugin contract

`github.com/webong/ext/pkg/plugin-go/contract`: the ext plugin contract and the tooling
built on it, using only the standard library (Go 1.23). It holds the contract
data model, host sessions and guests, `ext.package/v1` manifests and the installed
package store's building blocks, JSON-line and bridge transports, process launching,
schemas and typed authoring, instances and streams, and the conformance suite.

Subpackages: `bridge`, `inprocess`, `jsonline`, `jsonschema`, `packagekit`, `plugintest`, `schema`,
plus two groups. `guest` holds what a plugin implementation uses: `guest/author` (typed operations and
guest construction), `guest/capability` (the health and configuration contracts), `guest/instance`
(configured resources) and `guest/stream` (the pull-stream service); the `Guest` type itself is in the
root package beside the host's `Session`. `host` groups everything that runs plugins on a machine: `host` opens an installed package as a running plugin, `host/store` installs,
verifies and lists packages under a root you choose, `host/process` starts the child process,
`host/adapter` manages host-side adapters, `host/interop` resolves which backend serves a request
and `host/nativego` loads a Go plugin built as a shared library. All of it is pure Go. The
[`cengine`](../cengine) module has engine-routed variants of `host` and `host/store` with an identical API.

This is the lean choice. Add [`cengine`](../cengine) for the C engine binding,
[`wasm`](../wasm) for WebAssembly or [`hashicorp`](../hashicorp) for go-plugin, or
require the umbrella module `github.com/webong/ext/pkg/plugin-go` to take them all.
A backend never depends on `cengine`.
