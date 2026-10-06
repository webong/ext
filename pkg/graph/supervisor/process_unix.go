//go:build !windows

package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type unixProcessControl struct{ pid int }

func newProcessControl(cmd *exec.Cmd) (processControl, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return &unixProcessControl{}, nil
}

func (c *unixProcessControl) Attach(process *os.Process) error {
	c.pid = process.Pid
	return nil
}

func (c *unixProcessControl) Terminate(force bool) error {
	if c.pid <= 0 {
		return errors.New("process group is unavailable")
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	if err := syscall.Kill(-c.pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (*unixProcessControl) Close() error { return nil }

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminateOrphan(pid int, identity string) error {
	if !processAlive(pid) {
		return nil
	}
	current, err := processIdentity(pid)
	if err != nil || current != identity {
		return errors.New("orphan process identity changed")
	}
	group, err := syscall.Getpgid(pid)
	if err != nil || group != pid {
		return errors.New("orphan process is outside its isolated process group")
	}
	control := &unixProcessControl{pid: pid}
	if err := control.Terminate(false); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	return control.Terminate(true)
}
