// Build with: go build -buildmode=plugin -o echo.so ./examples/plugins/go/plugin-runtimes/nativego
package main

import (
	"context"
	"github.com/webong/ext/examples/plugins/go/plugin-runtimes/echo"
	cgoengine "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
)

func ExtPlugin(ctx context.Context) (cgoengine.Backend, error) { return echo.New(ctx) }
func main()                                                    {}
