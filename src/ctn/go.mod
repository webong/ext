module github.com/webong/ext/ctn

go 1.26.0

require (
	github.com/webong/ext/pkg/ext-go/engine v0.1.0
	github.com/webong/ext/res/system v0.1.1 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/ext/res/system => ../../res/system

replace github.com/webong/ext/pkg/ext-go/engine => ../../pkg/ext-go/engine
