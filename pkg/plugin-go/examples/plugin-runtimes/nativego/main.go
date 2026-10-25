// Build with: go build -buildmode=plugin -o echo.so ./pkg/plugin-go/examples/plugin-runtimes/nativego
package main

import (
	"context"
	"github.com/webong/ext/pkg/plugin-go/cgoengine"
	"github.com/webong/ext/pkg/plugin-go/examples/plugin-runtimes/echo"
)

func CTXPlugin(ctx context.Context) (cgoengine.Backend, error) { return echo.New(ctx) }
func main()                                                    {}
