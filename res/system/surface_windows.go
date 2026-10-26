//go:build windows

package system

import (
	"context"
	"fmt"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	surfaceUser32                 = windows.NewLazySystemDLL("user32.dll")
	surfaceEnumDisplayMonitors    = surfaceUser32.NewProc("EnumDisplayMonitors")
	surfaceGetMonitorInfo         = surfaceUser32.NewProc("GetMonitorInfoW")
	surfaceEnumWindows            = surfaceUser32.NewProc("EnumWindows")
	surfaceIsWindowVisible        = surfaceUser32.NewProc("IsWindowVisible")
	surfaceGetWindowRect          = surfaceUser32.NewProc("GetWindowRect")
	surfaceGetWindowThreadProcess = surfaceUser32.NewProc("GetWindowThreadProcessId")
)

type surfaceWindowsRect struct{ Left, Top, Right, Bottom int32 }

type surfaceMonitorInfo struct {
	Size    uint32
	Monitor surfaceWindowsRect
	Work    surfaceWindowsRect
	Flags   uint32
}

func platformSurfaces(ctx context.Context, maxDisplays, maxWindows int) ([]DisplayInfo, CollectionStatus, []WindowInfo, CollectionStatus, string, error) {
	displays, displayStatus, err := windowsDisplays(ctx, maxDisplays)
	if err != nil {
		return nil, CollectionStatus{}, nil, CollectionStatus{}, "", err
	}
	windows, windowStatus, err := windowsWindows(ctx, maxWindows)
	return displays, displayStatus, windows, windowStatus, "virtual-desktop-pixels", err
}

func windowsDisplays(ctx context.Context, limit int) ([]DisplayInfo, CollectionStatus, error) {
	result := []DisplayInfo{}
	truncated := false
	var callbackErr error
	callback := syscall.NewCallback(func(monitor, _, _ uintptr, _ uintptr) uintptr {
		if err := ctx.Err(); err != nil {
			callbackErr = err
			return 0
		}
		if len(result) >= limit {
			truncated = true
			return 1
		}
		info := surfaceMonitorInfo{Size: uint32(unsafe.Sizeof(surfaceMonitorInfo{}))}
		ok, _, callErr := surfaceGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
		if ok == 0 {
			callbackErr = callErr
			return 0
		}
		bounds := SurfaceBounds{X: int(info.Monitor.Left), Y: int(info.Monitor.Top), Width: int(info.Monitor.Right - info.Monitor.Left), Height: int(info.Monitor.Bottom - info.Monitor.Top)}
		if bounds.Width > 0 && bounds.Height > 0 {
			result = append(result, DisplayInfo{ID: surfaceID("display", strconv.FormatUint(uint64(monitor), 10)), Bounds: bounds, Primary: info.Flags&1 != 0})
		}
		return 1
	})
	ok, _, callErr := surfaceEnumDisplayMonitors.Call(0, 0, callback, 0)
	if callbackErr != nil {
		return nil, CollectionStatus{}, callbackErr
	}
	if ok == 0 {
		return []DisplayInfo{}, surfaceStatus("unavailable", fmt.Sprintf("EnumDisplayMonitors failed: %v", callErr)), nil
	}
	if truncated {
		return result, surfaceStatus("partial", "display limit reached"), nil
	}
	return result, surfaceStatus("complete", ""), nil
}

func windowsWindows(ctx context.Context, limit int) ([]WindowInfo, CollectionStatus, error) {
	result := []WindowInfo{}
	starts := map[int]string{}
	truncated := false
	var callbackErr error
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		if err := ctx.Err(); err != nil {
			callbackErr = err
			return 0
		}
		visible, _, _ := surfaceIsWindowVisible.Call(window)
		if visible == 0 {
			return 1
		}
		var rect surfaceWindowsRect
		ok, _, _ := surfaceGetWindowRect.Call(window, uintptr(unsafe.Pointer(&rect)))
		if ok == 0 || rect.Right <= rect.Left || rect.Bottom <= rect.Top {
			return 1
		}
		var processID uint32
		surfaceGetWindowThreadProcess.Call(window, uintptr(unsafe.Pointer(&processID)))
		if processID == 0 {
			return 1
		}
		if len(result) >= limit {
			truncated = true
			return 1
		}
		pid := int(processID)
		startID, exists := starts[pid]
		if !exists {
			process, err := platformProcess(ctx, pid)
			if err == nil {
				startID = process.StartID
			}
			starts[pid] = startID
		}
		if startID == "" {
			return 1
		}
		bounds := SurfaceBounds{X: int(rect.Left), Y: int(rect.Top), Width: int(rect.Right - rect.Left), Height: int(rect.Bottom - rect.Top)}
		result = append(result, WindowInfo{ID: surfaceWindowID(pid, startID, strconv.FormatUint(uint64(window), 10)), PID: pid, ProcessStartID: startID, Bounds: bounds, OnScreen: true})
		return 1
	})
	ok, _, callErr := surfaceEnumWindows.Call(callback, 0)
	if callbackErr != nil {
		return nil, CollectionStatus{}, callbackErr
	}
	if ok == 0 {
		return []WindowInfo{}, surfaceStatus("unavailable", fmt.Sprintf("EnumWindows failed: %v", callErr)), nil
	}
	if truncated {
		return result, surfaceStatus("partial", "window limit reached"), nil
	}
	return result, surfaceStatus("complete", ""), nil
}
