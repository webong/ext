module github.com/webong/ctx/src/ctx

go 1.23.0

require (
	github.com/webong/ctx/pkg/graph v0.1.0
	github.com/webong/ctx/pkg/plugin v0.1.0
	github.com/webong/ctx/res/browser v0.1.0
)

require golang.org/x/sys v0.48.0 // indirect

replace github.com/webong/ctx/adapters/chromium => ../../adapters/chromium

replace github.com/webong/ctx/adapters/git => ../../adapters/git

replace github.com/webong/ctx/adapters/zen => ../../adapters/zen

replace github.com/webong/ctx/adapters/waterfox => ../../adapters/waterfox

replace github.com/webong/ctx/adapters/librewolf => ../../adapters/librewolf

replace github.com/webong/ctx/adapters/floorp => ../../adapters/floorp

replace github.com/webong/ctx/adapters/whale => ../../adapters/whale

replace github.com/webong/ctx/adapters/vivaldi => ../../adapters/vivaldi

replace github.com/webong/ctx/adapters/opera => ../../adapters/opera

replace github.com/webong/ctx/adapters/helium => ../../adapters/helium

replace github.com/webong/ctx/adapters/dia => ../../adapters/dia

replace github.com/webong/ctx/adapters/comet => ../../adapters/comet

replace github.com/webong/ctx/adapters/atlas => ../../adapters/atlas

replace github.com/webong/ctx/adapters/arc => ../../adapters/arc

replace github.com/webong/ctx/adapters/brave => ../../adapters/brave

replace github.com/webong/ctx/adapters/edge => ../../adapters/edge

replace github.com/webong/ctx/adapters/chrome => ../../adapters/chrome

replace github.com/webong/ctx/adapters/firefox => ../../adapters/firefox

replace github.com/webong/ctx/adapters/safari => ../../adapters/safari

replace github.com/webong/ctx/adapters/keychain => ../../adapters/keychain

replace github.com/webong/ctx/adapters/credman => ../../adapters/credman

replace github.com/webong/ctx/adapters/secret_service => ../../adapters/secret_service

replace github.com/webong/ctx/pkg/plugin-go => ../../pkg/plugin-go

replace github.com/webong/ctx/pkg/graph => ../../pkg/graph

replace github.com/webong/ctx/pkg/plugin => ../../pkg/plugin

replace github.com/webong/ctx/pkg/plugin-cshared => ../../pkg/plugin-cshared

replace github.com/webong/ctx/pkg/plugin-hashicorp => ../../pkg/plugin-hashicorp

replace github.com/webong/ctx/pkg/plugin-wasm => ../../pkg/plugin-wasm

replace github.com/webong/ctx/res/browser => ../../res/browser

replace github.com/webong/ctx/res/credential => ../../res/credential
