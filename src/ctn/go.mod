module github.com/webong/ext/ctn

go 1.26.0

require github.com/webong/ext/pkg/plugin v0.2.0

require (
	github.com/webong/ext/pkg/graph v0.1.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/ext/pkg/graph => ../../pkg/graph

replace github.com/webong/ext/pkg/plugin => ../../pkg/plugin
