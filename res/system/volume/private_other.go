//go:build !unix && !windows

package volume

import "os"

func isReparsePoint(_ string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}

func isMountRoot(string) bool { return false }
