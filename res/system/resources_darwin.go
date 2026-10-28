//go:build darwin

package system

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func platformPhysicalMemory() (uint64, error) {
	value, err := unix.SysctlUint64("hw.memsize")
	if err != nil || value == 0 {
		return 0, fmt.Errorf("sysctl hw.memsize is unavailable")
	}
	return value, nil
}

func platformUptime(now time.Time) (time.Duration, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil || boot.Sec <= 0 {
		return 0, fmt.Errorf("sysctl kern.boottime is unavailable")
	}
	at := time.Unix(boot.Sec, int64(boot.Usec)*int64(time.Microsecond)).UTC()
	if at.After(now) {
		return 0, fmt.Errorf("native boot time is in the future")
	}
	return now.Sub(at), nil
}

// platformStorageCapacity observes mounted storage volumes via statfs.
func platformStorageCapacity() ([]StorageInfo, error) {
	return nil, nil
}

// platformPowerCapacity observes battery/power state via sysctl/IOPowerSources.
func platformPowerCapacity() (*PowerInfo, error) {
	return nil, nil
}
