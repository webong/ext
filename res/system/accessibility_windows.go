//go:build windows

package system

import (
	"context"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	accessibilityGetForegroundWindow = surfaceUser32.NewProc("GetForegroundWindow")
	accessibilityEnumChildWindows    = surfaceUser32.NewProc("EnumChildWindows")
	accessibilityGetParent           = surfaceUser32.NewProc("GetParent")
	accessibilityGetClassName        = surfaceUser32.NewProc("GetClassNameW")
	accessibilityGetControlID        = surfaceUser32.NewProc("GetDlgCtrlID")
	accessibilityIsWindowEnabled     = surfaceUser32.NewProc("IsWindowEnabled")
)

func platformAccessibility(ctx context.Context, options AccessibilityOptions) ([]AccessibilityElement, CollectionStatus, error) {
	window, _, _ := accessibilityGetForegroundWindow.Call()
	if window == 0 {
		return []AccessibilityElement{}, surfaceStatus("unavailable", "focused window is unavailable"), nil
	}
	var processID uint32
	surfaceGetWindowThreadProcess.Call(window, uintptr(unsafe.Pointer(&processID)))
	if processID == 0 {
		return []AccessibilityElement{}, surfaceStatus("unavailable", "focused application identity is unavailable"), nil
	}
	pid := int(processID)
	process, err := platformProcess(ctx, pid)
	if err != nil || process.StartID == "" {
		return []AccessibilityElement{}, surfaceStatus("unavailable", "focused application generation is unavailable"), nil
	}
	handles := []uintptr{window}
	truncated := false
	var callbackErr error
	callback := syscall.NewCallback(func(child, _ uintptr) uintptr {
		if err := ctx.Err(); err != nil {
			callbackErr = err
			return 0
		}
		if windowsAccessibilityDepth(child, window) > options.MaxDepth {
			truncated = true
			return 1
		}
		if len(handles) >= options.MaxElements {
			truncated = true
			return 0
		}
		handles = append(handles, child)
		return 1
	})
	accessibilityEnumChildWindows.Call(window, callback, 0)
	if callbackErr != nil {
		return nil, CollectionStatus{}, callbackErr
	}
	ids := make(map[uintptr]string, len(handles))
	for _, handle := range handles {
		ids[handle] = accessibilityElementID(pid, process.StartID, strconv.FormatUint(uint64(handle), 10))
	}
	result := make([]AccessibilityElement, 0, len(handles))
	for _, handle := range handles {
		if err := ctx.Err(); err != nil {
			return nil, CollectionStatus{}, err
		}
		item := AccessibilityElement{
			ID: ids[handle], PID: pid, ProcessStartID: process.StartID,
			Role: windowsClassName(handle), Actions: []string{},
		}
		if item.Role == "" {
			item.Role = "window"
		}
		parent, _, _ := accessibilityGetParent.Call(handle)
		item.ParentID = ids[parent]
		controlID, _, _ := accessibilityGetControlID.Call(handle)
		if int32(controlID) != 0 {
			item.Identifier = strconv.FormatInt(int64(int32(controlID)), 10)
		}
		var rect surfaceWindowsRect
		if ok, _, _ := surfaceGetWindowRect.Call(handle, uintptr(unsafe.Pointer(&rect))); ok != 0 && rect.Right > rect.Left && rect.Bottom > rect.Top {
			bounds := SurfaceBounds{X: int(rect.Left), Y: int(rect.Top), Width: int(rect.Right - rect.Left), Height: int(rect.Bottom - rect.Top)}
			item.Bounds = &bounds
		}
		enabled, _, _ := accessibilityIsWindowEnabled.Call(handle)
		value := enabled != 0
		item.Enabled = &value
		focused := handle == window
		item.Focused = &focused
		result = append(result, item)
	}
	if truncated {
		return result, surfaceStatus("partial", "accessibility element limit reached"), nil
	}
	return result, surfaceStatus("complete", ""), nil
}

func windowsAccessibilityDepth(window, root uintptr) int {
	depth := 0
	for window != 0 && window != root {
		parent, _, _ := accessibilityGetParent.Call(window)
		window = parent
		depth++
		if depth > 65536 {
			break
		}
	}
	return depth
}

func windowsClassName(window uintptr) string {
	buffer := make([]uint16, 256)
	length, _, _ := accessibilityGetClassName.Call(window, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:length])
}

func platformPermissions(ctx context.Context) (PermissionInfo, PermissionInfo, error) {
	if err := ctx.Err(); err != nil {
		return PermissionInfo{}, PermissionInfo{}, err
	}
	return PermissionInfo{State: "not-required", Scope: "windows.ui-observation"}, PermissionInfo{State: "not-required", Scope: "windows.graphics-capture"}, nil
}
