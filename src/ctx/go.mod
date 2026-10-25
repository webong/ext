module github.com/webong/ext/ctx

go 1.26.0

require (
	github.com/webong/ext/res/system v0.1.1
	github.com/webong/ext/res/web v0.5.1
)

require (
	github.com/webong/ext/pkg/plugin-go/engine v0.1.0
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/ext/res/system => ../../res/system

replace github.com/webong/ext/res/web => ../../res/web

replace github.com/webong/ext/pkg/plugin-go/engine => ../../pkg/plugin-go/engine
