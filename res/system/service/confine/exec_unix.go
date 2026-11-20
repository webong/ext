//go:build darwin || linux

package confine

import (
	"fmt"
	"os"
	"syscall"

	"github.com/webong/ext/res/sandbox"
)

// applyRlimits sets the bounds a resource limit can hold exactly: open files, and CPU time in
// whole seconds, rounded up.
func applyRlimits(r sandbox.Resources) error {
	set := func(resource int, value uint64) error {
		limit := syscall.Rlimit{Cur: value, Max: value}
		if err := syscall.Setrlimit(resource, &limit); err != nil {
			return fmt.Errorf("%w: setrlimit", ErrDriver)
		}
		return nil
	}
	if r.OpenFiles != nil {
		if err := set(syscall.RLIMIT_NOFILE, uint64(*r.OpenFiles)); err != nil {
			return err
		}
	}
	if r.CPUTimeMs != nil {
		if err := set(syscall.RLIMIT_CPU, uint64((*r.CPUTimeMs+999)/1000)); err != nil {
			return err
		}
	}
	return nil
}

// start replaces the process with the guest. It returns only when exec fails.
func start(p plan) (int, error) {
	if p.Dir != "" {
		if err := os.Chdir(p.Dir); err != nil {
			return 0, fmt.Errorf("%w: chdir", ErrDriver)
		}
	}
	err := syscall.Exec(p.Path, append([]string{p.Path}, p.Args...), p.env)
	return 0, fmt.Errorf("%w: exec: %v", ErrDriver, err)
}
