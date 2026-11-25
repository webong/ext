module github.com/webong/ext/cmd/ext

go 1.26.0

require (
	github.com/webong/ext/pkg/ext-go/cengine v0.1.0
	github.com/webong/ext/pkg/ext-go/engine v0.4.0
	github.com/webong/ext/res/system v0.5.1
)

require github.com/ebitengine/purego v0.11.3 // indirect

replace github.com/webong/ext/pkg/ext-go/engine => ../../pkg/ext-go/engine

replace github.com/webong/ext/pkg/ext-go/cengine => ../../pkg/ext-go/cengine
