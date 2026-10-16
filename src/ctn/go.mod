module github.com/webong/ext/ctn

go 1.26.0

require (
	github.com/webong/ext/pkg/plugin-go/contract v0.2.0
	github.com/webong/ext/res/graph v0.1.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/ext/res/graph => ../../res/graph

replace github.com/webong/ext/pkg/plugin-go => ../../pkg/plugin-go

replace github.com/webong/ext/pkg/plugin-go/contract => ../../pkg/plugin-go/contract

replace github.com/webong/ext/pkg/plugin-go/cengine => ../../pkg/plugin-go/cengine

replace github.com/webong/ext/pkg/plugin-go/wasm => ../../pkg/plugin-go/wasm

replace github.com/webong/ext/pkg/plugin-go/hashicorp => ../../pkg/plugin-go/hashicorp
