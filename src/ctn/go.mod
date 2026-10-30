module github.com/webong/ext/ctn

go 1.26.0

require (
	github.com/webong/ext/pkg/ext-go/engine v0.1.0
	github.com/webong/ext/pkg/ext-go/cgoengine v0.1.0
	github.com/webong/ext/pkg/ext-go/model v0.1.0
	github.com/webong/ext/pkg/ext-go/perception v0.1.0
	github.com/webong/ext/res/int v0.1.0
	github.com/webong/ext/res/system v0.2.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/ext/res/int => ../../res/int
replace github.com/webong/ext/res/system => ../../res/system

replace github.com/webong/ext/pkg/ext-go/engine => ../../pkg/ext-go/engine
replace github.com/webong/ext/pkg/ext-go/cgoengine => ../../pkg/ext-go/cgoengine
replace github.com/webong/ext/pkg/ext-go/model => ../../pkg/ext-go/model
replace github.com/webong/ext/pkg/ext-go/perception => ../../pkg/ext-go/perception
