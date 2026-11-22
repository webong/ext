//go:build darwin || linux

package pkgcmd

import (
	"errors"
	"os/exec"
	"syscall"
)

func brokenPipeExit(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGPIPE
}
