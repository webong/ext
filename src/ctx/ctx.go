// Package ctx is the context manager as a library: Run is the whole command, so the
// standalone ctx binary and any host that composes it (such as ext) run the same code.
package ctx

import (
	"io"

	"github.com/webong/ext/ctx/internal/app"
)

// Run executes the ctx command with args (without the program name), writing to
// stdout and stderr, and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return app.Run(args, stdout, stderr)
}
