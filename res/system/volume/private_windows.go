//go:build windows

package volume

import (
	"os"

	"golang.org/x/sys/windows"
)

// isReparsePoint detects junctions, mount points, and other reparse points that
// os.FileInfo may present as ordinary directories.
func isReparsePoint(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return false
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// isMountRoot is unnecessary on Windows: drive roots are rejected as broad
// targets and a mounted folder is caught as a reparse point.
func isMountRoot(string) bool { return false }
