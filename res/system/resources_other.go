//go:build !darwin && !linux && !windows

package system

import "time"

func platformPhysicalMemory() (uint64, error) {
	return 0, errResourceUnsupported
}

func platformUptime(time.Time) (time.Duration, error) {
	return 0, errResourceUnsupported
}

// platformStorageCapacity observes mounted storage volumes.
func platformStorageCapacity() ([]StorageInfo, error) {
	return nil, nil
}

// platformPowerCapacity observes battery/power state.
func platformPowerCapacity() (*PowerInfo, error) {
	return nil, nil
}
