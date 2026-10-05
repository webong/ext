// Build with GOOS=wasip1 GOARCH=wasm. Diagnostics go to stderr; stdout is protocol.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/webong/ctx/examples/plugin-runtimes/echo"
	"github.com/webong/ctx/pkg/plugin/jsonline"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	ctx := context.Background()
	g, err := echo.New(ctx)
	if err != nil {
		return err
	}
	defer g.Close()
	return jsonline.ServeStdio(ctx, g)
}
