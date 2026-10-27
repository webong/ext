//go:build windows

package system

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	resourceKernel32               = windows.NewLazySystemDLL("kernel32.dll")
	resourceGlobalMemoryStatusEx   = resourceKernel32.NewProc("GlobalMemoryStatusEx")
	resourceQueryUnbiasedInterrupt = resourceKernel32.NewProc("QueryUnbiasedInterruptTime")
)

type resourceMemoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func platformPhysicalMemory() (uint64, error) {
	status := resourceMemoryStatusEx{Length: uint32(unsafe.Sizeof(resourceMemoryStatusEx{}))}
	result, _, callErr := resourceGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 || status.TotalPhys == 0 {
		return 0, fmt.Errorf("GlobalMemoryStatusEx is unavailable: %v", callErr)
	}
	return status.TotalPhys, nil
}

func platformUptime(time.Time) (time.Duration, error) {
	var unbiased uint64
	result, _, callErr := resourceQueryUnbiasedInterrupt.Call(uintptr(unsafe.Pointer(&unbiased)))
	if result == 0 || unbiased == 0 {
		return 0, fmt.Errorf("QueryUnbiasedInterruptTime is unavailable: %v", callErr)
	}
	// The counter is in 100-nanosecond units.
	return time.Duration(unbiased * 100), nil
}
