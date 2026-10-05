// Build with: go build -buildmode=plugin -o echo.so ./examples/plugin-runtimes/nativego
package main

import (
	"context"
	"github.com/webong/ctx/examples/plugin-runtimes/echo"
	"github.com/webong/ctx/pkg/plugin"
)

func CTXPlugin(ctx context.Context) (plugin.Backend, error) { return echo.New(ctx) }
func main()                                                 {}
