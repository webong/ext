//go:build darwin && cgo

package system

/*
#cgo LDFLAGS: -framework CoreFoundation -framework CoreGraphics
#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CoreGraphics.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct {
    uint32_t identifier;
    double x;
    double y;
    double width;
    double height;
    double scale;
    int primary;
} ext_system_display;

typedef struct {
    uint32_t identifier;
    int32_t pid;
    double x;
    double y;
    double width;
    double height;
} ext_system_window;

static int ext_system_displays(int limit, ext_system_display **output, int *written, int *total) {
    uint32_t count = 0;
    CGError error = CGGetActiveDisplayList(0, NULL, &count);
    if (error != kCGErrorSuccess) return (int)error;
    *total = (int)count;
    uint32_t capacity = count < (uint32_t)limit ? count : (uint32_t)limit;
    if (capacity == 0) { *output = NULL; *written = 0; return 0; }
    CGDirectDisplayID *identifiers = calloc(count, sizeof(CGDirectDisplayID));
    ext_system_display *values = calloc(capacity, sizeof(ext_system_display));
    if (identifiers == NULL || values == NULL) { free(identifiers); free(values); return -1; }
    error = CGGetActiveDisplayList(count, identifiers, &count);
    if (error != kCGErrorSuccess) { free(identifiers); free(values); return (int)error; }
    for (uint32_t index = 0; index < capacity; index++) {
        CGRect bounds = CGDisplayBounds(identifiers[index]);
        values[index].identifier = identifiers[index];
        values[index].x = bounds.origin.x;
        values[index].y = bounds.origin.y;
        values[index].width = bounds.size.width;
        values[index].height = bounds.size.height;
        values[index].scale = bounds.size.width > 0 ? (double)CGDisplayPixelsWide(identifiers[index]) / bounds.size.width : 0;
        values[index].primary = identifiers[index] == CGMainDisplayID();
    }
    free(identifiers);
    *output = values;
    *written = (int)capacity;
    return 0;
}

static int ext_system_windows(int limit, ext_system_window **output, int *written, int *total) {
    CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
    if (list == NULL) return -2;
    CFIndex count = CFArrayGetCount(list);
    ext_system_window *values = limit == 0 ? NULL : calloc((size_t)limit, sizeof(ext_system_window));
    if (limit != 0 && values == NULL) { CFRelease(list); return -1; }
    int found = 0;
    int stored = 0;
    for (CFIndex index = 0; index < count; index++) {
        CFDictionaryRef item = (CFDictionaryRef)CFArrayGetValueAtIndex(list, index);
        int32_t pid = 0;
        uint32_t identifier = 0;
        CGRect bounds = CGRectZero;
        CFNumberRef pidValue = (CFNumberRef)CFDictionaryGetValue(item, kCGWindowOwnerPID);
        CFNumberRef idValue = (CFNumberRef)CFDictionaryGetValue(item, kCGWindowNumber);
        CFDictionaryRef boundsValue = (CFDictionaryRef)CFDictionaryGetValue(item, kCGWindowBounds);
        if (pidValue == NULL || idValue == NULL || boundsValue == NULL ||
            !CFNumberGetValue(pidValue, kCFNumberSInt32Type, &pid) ||
            !CFNumberGetValue(idValue, kCFNumberSInt32Type, &identifier) ||
            !CGRectMakeWithDictionaryRepresentation(boundsValue, &bounds) ||
            pid <= 0 || bounds.size.width <= 0 || bounds.size.height <= 0) continue;
        found++;
        if (stored >= limit) continue;
        values[stored].identifier = identifier;
        values[stored].pid = pid;
        values[stored].x = bounds.origin.x;
        values[stored].y = bounds.origin.y;
        values[stored].width = bounds.size.width;
        values[stored].height = bounds.size.height;
        stored++;
    }
    CFRelease(list);
    *output = values;
    *written = stored;
    *total = found;
    return 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"strconv"
	"unsafe"
)

func platformSurfaces(ctx context.Context, maxDisplays, maxWindows int) ([]DisplayInfo, CollectionStatus, []WindowInfo, CollectionStatus, string, error) {
	displays, displayStatus := darwinDisplays(maxDisplays)
	windows, windowStatus, err := darwinWindows(ctx, maxWindows)
	return displays, displayStatus, windows, windowStatus, "desktop-global-points", err
}

func darwinDisplays(limit int) ([]DisplayInfo, CollectionStatus) {
	var values *C.ext_system_display
	var written, total C.int
	code := C.ext_system_displays(C.int(limit), &values, &written, &total)
	if values != nil {
		defer C.free(unsafe.Pointer(values))
	}
	if code != 0 {
		return []DisplayInfo{}, surfaceStatus("unavailable", fmt.Sprintf("CoreGraphics display enumeration failed (%d)", int(code)))
	}
	result := make([]DisplayInfo, 0, int(written))
	for _, value := range unsafe.Slice(values, int(written)) {
		bounds := SurfaceBounds{X: int(value.x), Y: int(value.y), Width: int(value.width), Height: int(value.height)}
		if bounds.Width <= 0 || bounds.Height <= 0 {
			continue
		}
		result = append(result, DisplayInfo{ID: surfaceID("display", strconv.FormatUint(uint64(value.identifier), 10)), Bounds: bounds, Scale: float64(value.scale), Primary: value.primary != 0})
	}
	if total > written {
		return result, surfaceStatus("partial", "display limit reached")
	}
	return result, surfaceStatus("complete", "")
}

func darwinWindows(ctx context.Context, limit int) ([]WindowInfo, CollectionStatus, error) {
	var values *C.ext_system_window
	var written, total C.int
	code := C.ext_system_windows(C.int(limit), &values, &written, &total)
	if values != nil {
		defer C.free(unsafe.Pointer(values))
	}
	if code != 0 {
		return []WindowInfo{}, surfaceStatus("unavailable", fmt.Sprintf("CoreGraphics window enumeration failed (%d)", int(code))), nil
	}
	starts := map[int]string{}
	result := make([]WindowInfo, 0, int(written))
	identityUnavailable := false
	for _, value := range unsafe.Slice(values, int(written)) {
		if err := ctx.Err(); err != nil {
			return nil, CollectionStatus{}, err
		}
		pid := int(value.pid)
		startID, ok := starts[pid]
		if !ok {
			process, err := platformProcess(ctx, pid)
			if err == nil {
				startID = process.StartID
			}
			starts[pid] = startID
		}
		if startID == "" {
			identityUnavailable = true
			continue
		}
		bounds := SurfaceBounds{X: int(value.x), Y: int(value.y), Width: int(value.width), Height: int(value.height)}
		if bounds.Width <= 0 || bounds.Height <= 0 {
			continue
		}
		native := strconv.FormatUint(uint64(value.identifier), 10)
		result = append(result, WindowInfo{ID: surfaceWindowID(pid, startID, native), PID: pid, ProcessStartID: startID, Bounds: bounds, OnScreen: true})
	}
	if total > written {
		return result, surfaceStatus("partial", "window limit reached"), nil
	}
	if identityUnavailable {
		return result, surfaceStatus("partial", "some window process generations were unavailable"), nil
	}
	return result, surfaceStatus("complete", ""), nil
}
