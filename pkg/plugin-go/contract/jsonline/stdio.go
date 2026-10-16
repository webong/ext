package jsonline

import (
	"context"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"os"
)

// ServeStdio serves a standalone guest on stdin/stdout, including Go WASI
// commands. It owns and closes both streams. Send diagnostics to stderr only;
// stdout is reserved for the CTX protocol. Call once from the guest's main.
func ServeStdio(ctx context.Context, guest plugin.Endpoint) error {
	return ServeGuest(ctx, stdio{}, guest)
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return errors.Join(os.Stdin.Close(), os.Stdout.Close()) }
