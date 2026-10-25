//go:build darwin || linux

package c2goengine

import (
	"runtime"
	"syscall"
)

// peakResidentBytes is the largest resident size the process has had.
func peakResidentBytes() (uint64, bool) {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return 0, false
	}
	if runtime.GOOS == "linux" {
		return uint64(usage.Maxrss) << 10, true // kilobytes
	}
	return uint64(usage.Maxrss), true // bytes
}
