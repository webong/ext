//go:build !windows

package host

import "syscall"

// ExecutableAccess reports whether the calling user may execute the file at path. It does not run it.
func ExecutableAccess(path string) error {
	return syscall.Access(path, 1)
}

// DirectoryWritable reports whether the calling user may create entries in the directory at path, without creating a probe file.
func DirectoryWritable(path string) error {
	return syscall.Access(path, 3) // Write and traverse permission, without creating a probe file.
}
