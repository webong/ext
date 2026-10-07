module github.com/webong/ext/ctx

go 1.23.0

require (
	github.com/webong/ext/pkg/graph v0.1.0
	github.com/webong/ext/pkg/plugin v0.1.0
	github.com/webong/ext/res/web v0.1.0
)

require golang.org/x/sys v0.48.0 // indirect

replace github.com/webong/ext/adapters/chromium => ../../adapters/chromium

replace github.com/webong/ext/adapters/git => ../../adapters/git

replace github.com/webong/ext/adapters/zen => ../../adapters/zen

replace github.com/webong/ext/adapters/wasm => ../../adapters/wasm

replace github.com/webong/ext/adapters/evm => ../../adapters/evm

replace github.com/webong/ext/adapters/waterfox => ../../adapters/waterfox

replace github.com/webong/ext/adapters/librewolf => ../../adapters/librewolf

replace github.com/webong/ext/adapters/floorp => ../../adapters/floorp

replace github.com/webong/ext/adapters/whale => ../../adapters/whale

replace github.com/webong/ext/adapters/vivaldi => ../../adapters/vivaldi

replace github.com/webong/ext/adapters/opera => ../../adapters/opera

replace github.com/webong/ext/adapters/helium => ../../adapters/helium

replace github.com/webong/ext/adapters/dia => ../../adapters/dia

replace github.com/webong/ext/adapters/comet => ../../adapters/comet

replace github.com/webong/ext/adapters/atlas => ../../adapters/atlas

replace github.com/webong/ext/adapters/arc => ../../adapters/arc

replace github.com/webong/ext/adapters/brave => ../../adapters/brave

replace github.com/webong/ext/adapters/edge => ../../adapters/edge

replace github.com/webong/ext/adapters/chrome => ../../adapters/chrome

replace github.com/webong/ext/adapters/firefox => ../../adapters/firefox

replace github.com/webong/ext/adapters/safari => ../../adapters/safari

replace github.com/webong/ext/adapters/keychain => ../../adapters/keychain

replace github.com/webong/ext/adapters/credman => ../../adapters/credman

replace github.com/webong/ext/adapters/secret_service => ../../adapters/secret_service

replace github.com/webong/ext/pkg/plugin-go => ../../pkg/plugin-go

replace github.com/webong/ext/pkg/graph => ../../pkg/graph

replace github.com/webong/ext/pkg/plugin => ../../pkg/plugin

replace github.com/webong/ext/pkg/plugin-cshared => ../../pkg/plugin-cshared

replace github.com/webong/ext/pkg/plugin-hashicorp => ../../pkg/plugin-hashicorp

replace github.com/webong/ext/pkg/plugin-wasm => ../../pkg/plugin-wasm

replace github.com/webong/ext/res/web => ../../res/web

replace github.com/webong/ext/res/credential => ../../res/credential
