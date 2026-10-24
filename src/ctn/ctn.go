// Package ctn is the content manager as a library: Run is the whole command, so the
// standalone ctn binary and any host that composes it (such as ext) run the same code.
package ctn

import (
	"io"

	"github.com/webong/ext/ctn/internal/app"
)

// Run executes the ctn command with args (without the program name), writing to
// stdout and stderr, and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return app.Run(args, stdout, stderr)
}

// Version is the version this build of ctn reports.
func Version() string { return app.Version }
