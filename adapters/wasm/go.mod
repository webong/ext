module github.com/webong/ext/adapters/wasm

go 1.23.0

require github.com/webong/ext/pkg/plugin-go/wasm v0.2.1

require github.com/webong/ext/pkg/plugin-go/engine v0.1.0 // indirect

require (
	github.com/tetratelabs/wazero v1.10.1 // indirect
	github.com/webong/ext/pkg/plugin-go/cgoengine v0.1.0
)
