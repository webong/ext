//go:build !darwin && !linux && !windows

package system

import "time"

func platformPhysicalMemory() (uint64, error) {
	return 0, errResourceUnsupported
}

func platformUptime(time.Time) (time.Duration, error) {
	return 0, errResourceUnsupported
}
