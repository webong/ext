//go:build !windows

package systemgraph

import "syscall"

func hostExecutableAccess(path string) error {
	return syscall.Access(path, 1)
}

func hostDirectoryWritable(path string) error {
	return syscall.Access(path, 3) // Write and traverse permission, without creating a probe file.
}
