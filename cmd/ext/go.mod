module github.com/webong/ext/cmd/ext

go 1.26.0

require (
	github.com/webong/ext/ctn v0.0.0
	github.com/webong/ext/ctx v0.0.0
	github.com/webong/ext/pkg/ext-go/engine v0.1.0
)

require (
	github.com/webong/ext/res/content v0.1.0 // indirect
	github.com/webong/ext/res/ml v0.1.0 // indirect
	github.com/webong/ext/res/system v0.2.0 // indirect
	github.com/webong/ext/res/web v0.6.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/ext/ctx => ../../src/ctx

replace github.com/webong/ext/ctn => ../../src/ctn

replace github.com/webong/ext/res/content => ../../res/content
replace github.com/webong/ext/res/ml => ../../res/ml
replace github.com/webong/ext/res/system => ../../res/system

replace github.com/webong/ext/res/web => ../../res/web

replace github.com/webong/ext/pkg/ext-go/engine => ../../pkg/ext-go/engine
