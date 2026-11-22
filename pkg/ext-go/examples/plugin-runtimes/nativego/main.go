// Build with: go build -buildmode=plugin -o echo.so ./pkg/ext-go/examples/plugin-runtimes/nativego
package main

import (
	"context"
	cgoengine "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/examples/plugin-runtimes/echo"
)

func CTXPlugin(ctx context.Context) (cgoengine.Backend, error) { return echo.New(ctx) }
func main()                                                    {}
