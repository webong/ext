//go:build darwin || linux

package pkgcmd

import (
	"fmt"
	"os"
	"syscall"
)

func duplicateFile(n int) (*os.File, error) {
	copy, err := syscall.Dup(n)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(copy), fmt.Sprintf("command-fd-%d", n)), nil
}
