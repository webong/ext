//go:build linux

package system

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
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

// platformStorageCapacity observes mounted storage volumes via /proc/mounts.
func platformStorageCapacity() ([]StorageInfo, error) {
	return linuxStorageCapacity(os.ReadFile, statfs)
}

func statfs(path string) (*syscall.Statfs_t, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}
	return &stat, nil
}

// linuxStorageCapacity observes mounted storage volumes. readFile reads
// /proc/mounts; statfs returns filesystem statistics for a mount point.
// Both are injectable so the observation can be tested without a real host.
func linuxStorageCapacity(readFile func(string) ([]byte, error), statfs func(string) (*syscall.Statfs_t, error)) ([]StorageInfo, error) {
	payload, err := readFile("/proc/mounts")
	if err != nil {
		return nil, nil
	}
	var result []StorageInfo
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		device := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]
		info, err := statfs(mountPoint)
		if err != nil {
			continue
		}
		readonly := false
		for _, option := range strings.Split(fields[3], ",") {
			if option == "ro" {
				readonly = true
			}
		}
		result = append(result, StorageInfo{
			Device:         device,
			MountPoint:     mountPoint,
			TotalBytes:     uint64(info.Blocks) * uint64(info.Frsize),
			AvailableBytes: uint64(info.Bavail) * uint64(info.Frsize),
			Type:           fsType,
			ReadOnly:       readonly,
		})
	}
	return result, nil
}

// platformPowerCapacity observes battery/power state via /sys/class/power_supply.
func platformPowerCapacity() (*PowerInfo, error) {
	return linuxPowerCapacity(os.ReadDir, os.ReadFile)
}

// linuxPowerCapacity observes battery/power state. readDir lists power supply
// entries; readFile reads capacity and status. Both are injectable so the
// observation can be tested without a real host.
func linuxPowerCapacity(readDir func(string) ([]os.DirEntry, error), readFile func(string) ([]byte, error)) (*PowerInfo, error) {
	entries, err := readDir("/sys/class/power_supply")
	if err != nil {
		return nil, nil
	}
	var result PowerInfo
	any := false
	for _, entry := range entries {
		capacity, err := readFile("/sys/class/power_supply/" + entry.Name() + "/capacity")
		if err != nil {
			continue
		}
		percentage, parseErr := strconv.Atoi(strings.TrimSpace(string(capacity)))
		if parseErr != nil || percentage < 0 || percentage > 100 {
			continue
		}
		status, err := readFile("/sys/class/power_supply/" + entry.Name() + "/status")
		if err != nil {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(string(status)))
		any = true
		if percentage > result.Percentage {
			result.Percentage = percentage
		}
		switch state {
		case "charging":
			result.State = PowerStateCharging
		case "discharging":
			result.State = PowerStateDischarging
		case "full", "charged":
			result.State = PowerStateCharged
		default:
			if result.State == "" {
				result.State = PowerStateUnknown
			}
		}
	}
	if !any {
		return nil, nil
	}
	return &result, nil
}
