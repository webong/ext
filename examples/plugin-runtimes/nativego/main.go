// Build with: go build -buildmode=plugin -o echo.so ./examples/plugin-runtimes/nativego
package main

import (
	"context"
	"github.com/webong/ext/examples/plugin-runtimes/echo"
	goengine "github.com/webong/ext/pkg/plugin-go"
)

func CTXPlugin(ctx context.Context) (goengine.Backend, error) { return echo.New(ctx) }
func main()                                                   {}
