// Package cto is ext's containment product. It runs a guest under a
// src/cto/sandbox grant through the mechanisms in src/cto/internal/confine.
package cto

import (
	"io"

	"github.com/webong/ext/src/cto/internal/app"
)

// Run executes the cto command for the package guest or another Go host.
func Run(args []string, stdout, stderr io.Writer) int {
	return app.Run("cto", args, stdout, stderr)
}

// RunAs executes cto through a command prefix such as "ext sandbox".
func RunAs(name string, args []string, stdout, stderr io.Writer) int {
	return app.Run(name, args, stdout, stderr)
}

// Version is the version of this build of cto.
func Version() string { return app.Version }
