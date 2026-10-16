// Deprecated: use github.com/webong/ext/pkg/plugin-go/wasm. It is a module of its own under pkg/plugin-go and requires only the contract module. See docs/module-versioning.md in github.com/webong/ext for the full
// migration table.
module github.com/webong/ext/pkg/plugin-wasm

go 1.23.0

require (
	github.com/tetratelabs/wazero v1.10.1
	github.com/webong/ext/pkg/plugin v0.2.0
	github.com/webong/ext/pkg/plugin-cshared v0.1.0
	github.com/webong/ext/pkg/plugin-go v0.1.1
)

require github.com/webong/ext/pkg/graph v0.1.0 // indirect
