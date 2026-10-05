//go:build darwin || linux

package systemgraph

import (
	"golang.org/x/sys/unix"
	"os"
)

func mapProcessFixture(file *os.File) (func() error, error) {
	data, err := unix.Mmap(int(file.Fd()), 0, 4096, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return func() error { return unix.Munmap(data) }, nil
}
