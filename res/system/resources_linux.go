//go:build linux

package system

import (
	"fmt"
	"os"
	"time"
)

// platformPhysicalMemory is unused on Linux: memoryCapacity reads /proc/meminfo
// through the injectable probe so container limits stay testable.
func platformPhysicalMemory() (uint64, error) {
	return 0, errResourceUnsupported
}

func platformUptime(now time.Time) (time.Duration, error) {
	if payload, err := os.ReadFile("/proc/uptime"); err == nil {
		if value, parseErr := parseProcUptime(payload); parseErr == nil {
			return value, nil
		}
	}
	payload, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, fmt.Errorf("proc uptime is unreadable")
	}
	boot, err := parseProcBootTime(payload)
	if err != nil {
		return 0, err
	}
	if boot.After(now) {
		return 0, fmt.Errorf("native boot time is in the future")
	}
	return now.Sub(boot), nil
}
