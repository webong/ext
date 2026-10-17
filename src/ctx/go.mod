module github.com/webong/ext/ctx

go 1.26.0

require (
	github.com/webong/ext/res/system v0.1.0
	github.com/webong/ext/res/web v0.1.0
)

require (
	github.com/webong/ext/pkg/plugin-go/contract v0.4.0
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/ext/res/system => ../../res/system

replace github.com/webong/ext/res/web => ../../res/web

replace github.com/webong/ext/pkg/plugin-go/contract => ../../pkg/plugin-go/contract
