module github.com/webong/ext/adapters/wasm

go 1.25.0

require github.com/webong/ext/res/web/host v0.1.0

require github.com/webong/ext/pkg/ext-go/engine v0.2.0 // indirect

require (
	github.com/tetratelabs/wazero v1.10.1 // indirect
	github.com/webong/ext/pkg/ext-go/cgoengine v0.2.0
	github.com/webong/ext/pkg/ext-go/wasm v0.5.0
	github.com/webong/ext/src/cto/sandbox v0.1.0
)

require github.com/webong/ext/pkg/ext-go/core v0.1.0 // indirect

require github.com/webong/ext/pkg/ext-go/cengine v0.1.0 // indirect
