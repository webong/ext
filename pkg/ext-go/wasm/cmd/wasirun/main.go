// wasirun checks a portable C guest core compiled to WASI Preview 1.
package main

import (
	"context"
	"fmt"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("expected WASI guest-core test module")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(1024).WithCloseOnContextDone(true))
	defer r.Close(context.Background())
	if _, err = wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return err
	}
	_, err = r.InstantiateWithConfig(ctx, data, wazero.NewModuleConfig().WithStdout(os.Stdout).WithStderr(os.Stderr).WithSysWalltime().WithSysNanotime())
	return err
}
