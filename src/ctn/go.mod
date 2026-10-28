module github.com/webong/ext/ctn

go 1.26.0

require (
	github.com/webong/ext/pkg/ext-go/engine v0.1.0
	github.com/webong/ext/res/ml v0.1.0
	github.com/webong/ext/res/system v0.2.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

require github.com/webong/ext/res/ml v0.1.0 // indirect

replace github.com/webong/ext/res/ml => ../../res/ml
replace github.com/webong/ext/res/system => ../../res/system

replace github.com/webong/ext/pkg/ext-go/engine => ../../pkg/ext-go/engine
