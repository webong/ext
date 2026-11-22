//go:build !windows

package adapter

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func adapterCommand(path string, args []string) *exec.Cmd { return exec.Command(path, args...) }

func adapterCommandContext(ctx context.Context, path string, args []string) *exec.Cmd {
	if ctx == nil {
		return adapterCommand(path, args)
	}
	command := exec.CommandContext(ctx, path, args...)
	// A shell adapter can spawn credential or database helpers. Cancel the
	// invocation's own process group so those children cannot outlive a query.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
	return command
}
