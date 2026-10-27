//go:build unix

package volume

import (
	"os"
	"path/filepath"
	"syscall"
)

func isReparsePoint(_ string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}

// isMountRoot reports a path whose native device differs from its parent's, so
// removal cannot target a mounted volume root instead of a private directory.
func isMountRoot(path string) bool {
	parent := filepath.Dir(path)
	if parent == path {
		return true
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return false
	}
	left, leftOK := info.Sys().(*syscall.Stat_t)
	right, rightOK := parentInfo.Sys().(*syscall.Stat_t)
	return leftOK && rightOK && left.Dev != right.Dev
}
