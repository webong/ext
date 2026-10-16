# Go plugin contract

`github.com/webong/ext/pkg/plugin-go/contract`: the ext plugin contract and the tooling
built on it, using only the standard library (Go 1.23). It holds the contract
data model, host sessions and guests, `ext.package/v1` manifests and the installed
package store's building blocks, JSON-line and bridge transports, process launching,
schemas and typed authoring, instances and streams, and the conformance suite.

Subpackages: `adapter`, `author`, `bridge`, `capability`, `host`, `inprocess`, `instance`,
`interop`, `jsonline`, `jsonschema`, `nativego`, `packagekit`, `plugintest`,
`process`, `schema`, `store`, `stream`. `store` installs, verifies and lists packages under a
root you choose; `host` opens an installed package as a running plugin. Both are pure Go. The
[`cengine`](../cengine) module has engine-routed variants with an identical API.

This is the lean choice. Add [`cengine`](../cengine) for the C engine binding,
[`wasm`](../wasm) for WebAssembly or [`hashicorp`](../hashicorp) for go-plugin, or
require the umbrella module `github.com/webong/ext/pkg/plugin-go` to take them all.
A backend never depends on `cengine`.
