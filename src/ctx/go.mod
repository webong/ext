module github.com/webong/ext/ctx

go 1.26.0

require (
	github.com/webong/ext/res/system v0.4.0
	github.com/webong/ext/res/web/host v0.1.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.61.0 // indirect
)

require (
	github.com/webong/ext/pkg/ext-go/engine v0.1.0
	github.com/webong/ext/res/graph v0.2.0
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/webong/ext/res/system => ../../res/system

replace github.com/webong/ext/res/web/host => ../../res/web/host

replace github.com/webong/ext/pkg/ext-go/engine => ../../pkg/ext-go/engine

replace github.com/webong/ext/res/graph => ../../res/graph
