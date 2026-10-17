module github.com/webong/ext/examples

go 1.26.0

require (
	github.com/hashicorp/go-hclog v0.14.1
	github.com/hashicorp/go-plugin v1.6.3
	github.com/webong/ext/pkg/plugin-go/hashicorp v0.2.1
	github.com/webong/ext/pkg/plugin-go/wasm v0.2.1
	github.com/webong/ext/res/system v0.1.0
)

require (
	github.com/fatih/color v1.7.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/hashicorp/yamux v0.1.1 // indirect
	github.com/mattn/go-colorable v0.1.4 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/oklog/run v1.0.0 // indirect
	github.com/tetratelabs/wazero v1.10.1 // indirect
	github.com/webong/ext/pkg/plugin-go/cengine v0.4.0
	github.com/webong/ext/pkg/plugin-go/contract v0.4.0
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/webong/ext/adapters/chromium => ../adapters/chromium

replace github.com/webong/ext/adapters/git => ../adapters/git

replace github.com/webong/ext/adapters/zen => ../adapters/zen

replace github.com/webong/ext/adapters/wasm => ../adapters/wasm

replace github.com/webong/ext/adapters/evm => ../adapters/evm

replace github.com/webong/ext/adapters/waterfox => ../adapters/waterfox

replace github.com/webong/ext/adapters/librewolf => ../adapters/librewolf

replace github.com/webong/ext/adapters/floorp => ../adapters/floorp

replace github.com/webong/ext/adapters/whale => ../adapters/whale

replace github.com/webong/ext/adapters/vivaldi => ../adapters/vivaldi

replace github.com/webong/ext/adapters/opera => ../adapters/opera

replace github.com/webong/ext/adapters/helium => ../adapters/helium

replace github.com/webong/ext/adapters/dia => ../adapters/dia

replace github.com/webong/ext/adapters/comet => ../adapters/comet

replace github.com/webong/ext/adapters/atlas => ../adapters/atlas

replace github.com/webong/ext/adapters/arc => ../adapters/arc

replace github.com/webong/ext/adapters/brave => ../adapters/brave

replace github.com/webong/ext/adapters/edge => ../adapters/edge

replace github.com/webong/ext/adapters/chrome => ../adapters/chrome

replace github.com/webong/ext/adapters/firefox => ../adapters/firefox

replace github.com/webong/ext/adapters/safari => ../adapters/safari

replace github.com/webong/ext/adapters/keychain => ../adapters/keychain

replace github.com/webong/ext/adapters/credman => ../adapters/credman

replace github.com/webong/ext/adapters/secret_service => ../adapters/secret_service

replace github.com/webong/ext/pkg/plugin-go => ../pkg/plugin-go

replace github.com/webong/ext/res/system => ../res/system

replace github.com/webong/ext/pkg/plugin-go/hashicorp => ../pkg/plugin-go/hashicorp

replace github.com/webong/ext/pkg/plugin-go/wasm => ../pkg/plugin-go/wasm

replace github.com/webong/ext/res/web => ../res/web

replace github.com/webong/ext/res/credential => ../res/credential

replace github.com/webong/ext/pkg/plugin-go/contract => ../pkg/plugin-go/contract

replace github.com/webong/ext/pkg/plugin-go/cengine => ../pkg/plugin-go/cengine
