//go:build darwin && cgo

package system

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics
#include <ApplicationServices/ApplicationServices.h>
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static int ext_system_ax_trusted(void) { return AXIsProcessTrusted() ? 1 : 0; }
static int ext_system_screen_recording(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }

static uintptr_t ext_system_ax_focused_application(void) {
    AXUIElementRef system = AXUIElementCreateSystemWide();
    if (system == NULL) return 0;
    CFTypeRef value = NULL;
    AXError error = AXUIElementCopyAttributeValue(system, kAXFocusedApplicationAttribute, &value);
    CFRelease(system);
    if (error != kAXErrorSuccess || value == NULL || CFGetTypeID(value) != AXUIElementGetTypeID()) {
        if (value != NULL) CFRelease(value);
        return 0;
    }
    return (uintptr_t)value;
}

static void ext_system_ax_release(uintptr_t value) {
    if (value != 0) CFRelease((CFTypeRef)value);
}

static int ext_system_ax_pid(uintptr_t element, int32_t *pid) {
    return AXUIElementGetPid((AXUIElementRef)element, pid) == kAXErrorSuccess;
}

static CFStringRef ext_system_ax_attribute_name(int attribute) {
    switch (attribute) {
        case 1: return kAXRoleAttribute;
        case 2: return kAXIdentifierAttribute;
        case 3: return kAXTitleAttribute;
        case 4: return kAXValueAttribute;
        case 5: return kAXEnabledAttribute;
        case 6: return kAXFocusedAttribute;
        case 7: return kAXChildrenAttribute;
        case 8: return kAXPositionAttribute;
        case 9: return kAXSizeAttribute;
        default: return NULL;
    }
}

static int ext_system_ax_string(uintptr_t element, int attribute, char *output, size_t capacity) {
    CFStringRef name = ext_system_ax_attribute_name(attribute);
    if (name == NULL || capacity == 0) return 0;
    CFTypeRef value = NULL;
    if (AXUIElementCopyAttributeValue((AXUIElementRef)element, name, &value) != kAXErrorSuccess || value == NULL) return 0;
    int ok = CFGetTypeID(value) == CFStringGetTypeID() && CFStringGetCString((CFStringRef)value, output, capacity, kCFStringEncodingUTF8);
    CFRelease(value);
    return ok;
}

static int ext_system_ax_bool(uintptr_t element, int attribute, int *output) {
    CFStringRef name = ext_system_ax_attribute_name(attribute);
    CFTypeRef value = NULL;
    if (name == NULL || AXUIElementCopyAttributeValue((AXUIElementRef)element, name, &value) != kAXErrorSuccess || value == NULL) return 0;
    int ok = CFGetTypeID(value) == CFBooleanGetTypeID();
    if (ok) *output = CFBooleanGetValue((CFBooleanRef)value) ? 1 : 0;
    CFRelease(value);
    return ok;
}

static uintptr_t ext_system_ax_children(uintptr_t element) {
    CFTypeRef value = NULL;
    if (AXUIElementCopyAttributeValue((AXUIElementRef)element, kAXChildrenAttribute, &value) != kAXErrorSuccess || value == NULL) return 0;
    if (CFGetTypeID(value) != CFArrayGetTypeID()) { CFRelease(value); return 0; }
    return (uintptr_t)value;
}

static long ext_system_ax_array_count(uintptr_t array) {
    return array == 0 ? 0 : (long)CFArrayGetCount((CFArrayRef)array);
}

static uintptr_t ext_system_ax_array_element(uintptr_t array, long index) {
    if (array == 0 || index < 0 || index >= CFArrayGetCount((CFArrayRef)array)) return 0;
    CFTypeRef value = CFArrayGetValueAtIndex((CFArrayRef)array, index);
    if (value == NULL || CFGetTypeID(value) != AXUIElementGetTypeID()) return 0;
    CFRetain(value);
    return (uintptr_t)value;
}

static int ext_system_ax_bounds(uintptr_t element, double *x, double *y, double *width, double *height) {
    CFTypeRef position = NULL;
    CFTypeRef size = NULL;
    if (AXUIElementCopyAttributeValue((AXUIElementRef)element, kAXPositionAttribute, &position) != kAXErrorSuccess || position == NULL ||
        AXUIElementCopyAttributeValue((AXUIElementRef)element, kAXSizeAttribute, &size) != kAXErrorSuccess || size == NULL) {
        if (position != NULL) CFRelease(position);
        if (size != NULL) CFRelease(size);
        return 0;
    }
    CGPoint point = CGPointZero;
    CGSize dimensions = CGSizeZero;
    int ok = CFGetTypeID(position) == AXValueGetTypeID() && CFGetTypeID(size) == AXValueGetTypeID() &&
        AXValueGetType((AXValueRef)position) == kAXValueCGPointType && AXValueGetType((AXValueRef)size) == kAXValueCGSizeType &&
        AXValueGetValue((AXValueRef)position, kAXValueCGPointType, &point) &&
        AXValueGetValue((AXValueRef)size, kAXValueCGSizeType, &dimensions);
    CFRelease(position); CFRelease(size);
    if (!ok || dimensions.width <= 0 || dimensions.height <= 0) return 0;
    *x = point.x; *y = point.y; *width = dimensions.width; *height = dimensions.height;
    return 1;
}

static int ext_system_ax_actions(uintptr_t element, char *output, size_t capacity) {
    if (capacity == 0) return 0;
    CFArrayRef actions = NULL;
    if (AXUIElementCopyActionNames((AXUIElementRef)element, &actions) != kAXErrorSuccess || actions == NULL) return 0;
    output[0] = '\0';
    size_t used = 0;
    CFIndex count = CFArrayGetCount(actions);
    for (CFIndex index = 0; index < count; index++) {
        CFStringRef action = (CFStringRef)CFArrayGetValueAtIndex(actions, index);
        if (action == NULL || CFGetTypeID(action) != CFStringGetTypeID()) continue;
        char value[256];
        if (!CFStringGetCString(action, value, sizeof(value), kCFStringEncodingUTF8)) continue;
        size_t length = strlen(value);
        if (used + length + (used == 0 ? 1 : 2) > capacity) break;
        if (used != 0) output[used++] = '\n';
        memcpy(output + used, value, length);
        used += length;
        output[used] = '\0';
    }
    CFRelease(actions);
    return (int)used;
}
*/
import "C"

import (
	"context"
	"strconv"
	"strings"
	"unsafe"
)

type darwinAccessibilityNode struct {
	element  C.uintptr_t
	parentID string
	path     string
	depth    int
}

func platformAccessibility(ctx context.Context, options AccessibilityOptions) ([]AccessibilityElement, CollectionStatus, error) {
	if C.ext_system_ax_trusted() == 0 {
		return []AccessibilityElement{}, surfaceStatus("permission-denied", "macOS Accessibility access is not granted"), nil
	}
	root := C.ext_system_ax_focused_application()
	if root == 0 {
		return []AccessibilityElement{}, surfaceStatus("unavailable", "focused application is unavailable"), nil
	}
	defer C.ext_system_ax_release(root)
	var nativePID C.int32_t
	if C.ext_system_ax_pid(root, &nativePID) == 0 || nativePID <= 0 {
		return []AccessibilityElement{}, surfaceStatus("unavailable", "focused application identity is unavailable"), nil
	}
	pid := int(nativePID)
	process, err := platformProcess(ctx, pid)
	if err != nil || process.StartID == "" {
		return []AccessibilityElement{}, surfaceStatus("unavailable", "focused application generation is unavailable"), nil
	}
	queue := []darwinAccessibilityNode{{element: root, path: "0"}}
	result := make([]AccessibilityElement, 0, min(options.MaxElements, 128))
	truncated := false
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			for _, pending := range queue {
				if pending.element != root {
					C.ext_system_ax_release(pending.element)
				}
			}
			return nil, CollectionStatus{}, err
		}
		node := queue[0]
		queue = queue[1:]
		if len(result) >= options.MaxElements {
			truncated = true
			if node.element != root {
				C.ext_system_ax_release(node.element)
			}
			continue
		}
		id := accessibilityElementID(pid, process.StartID, node.path)
		item := AccessibilityElement{
			ID: id, ParentID: node.parentID, PID: pid, ProcessStartID: process.StartID,
			Role: darwinAXString(node.element, 1), Identifier: darwinAXString(node.element, 2),
			Actions: darwinAXActions(node.element),
		}
		if item.Role == "" {
			item.Role = "unknown"
		}
		if options.IncludeText {
			item.Label = darwinAXString(node.element, 3)
			item.Value = darwinAXString(node.element, 4)
		}
		item.Enabled = darwinAXBool(node.element, 5)
		item.Focused = darwinAXBool(node.element, 6)
		var x, y, width, height C.double
		if C.ext_system_ax_bounds(node.element, &x, &y, &width, &height) != 0 {
			bounds := SurfaceBounds{X: int(x), Y: int(y), Width: int(width), Height: int(height)}
			item.Bounds = &bounds
		}
		result = append(result, item)
		if node.depth < options.MaxDepth {
			children := C.ext_system_ax_children(node.element)
			if children != 0 {
				count := int(C.ext_system_ax_array_count(children))
				for index := 0; index < count; index++ {
					child := C.ext_system_ax_array_element(children, C.long(index))
					if child != 0 {
						queue = append(queue, darwinAccessibilityNode{element: child, parentID: id, path: node.path + "/" + strconv.Itoa(index), depth: node.depth + 1})
					}
				}
				C.ext_system_ax_release(children)
			}
		} else if children := C.ext_system_ax_children(node.element); children != 0 {
			truncated = truncated || C.ext_system_ax_array_count(children) > 0
			C.ext_system_ax_release(children)
		}
		if node.element != root {
			C.ext_system_ax_release(node.element)
		}
	}
	if truncated {
		return result, surfaceStatus("partial", "accessibility element or depth limit reached"), nil
	}
	return result, surfaceStatus("complete", ""), nil
}

func darwinAXString(element C.uintptr_t, attribute int) string {
	buffer := make([]byte, 4096)
	if C.ext_system_ax_string(element, C.int(attribute), (*C.char)(unsafe.Pointer(&buffer[0])), C.size_t(len(buffer))) == 0 {
		return ""
	}
	return C.GoString((*C.char)(unsafe.Pointer(&buffer[0])))
}

func darwinAXBool(element C.uintptr_t, attribute int) *bool {
	var value C.int
	if C.ext_system_ax_bool(element, C.int(attribute), &value) == 0 {
		return nil
	}
	result := value != 0
	return &result
}

func darwinAXActions(element C.uintptr_t) []string {
	buffer := make([]byte, 8192)
	if C.ext_system_ax_actions(element, (*C.char)(unsafe.Pointer(&buffer[0])), C.size_t(len(buffer))) == 0 {
		return nil
	}
	return strings.Split(C.GoString((*C.char)(unsafe.Pointer(&buffer[0]))), "\n")
}

func platformPermissions(ctx context.Context) (PermissionInfo, PermissionInfo, error) {
	if err := ctx.Err(); err != nil {
		return PermissionInfo{}, PermissionInfo{}, err
	}
	accessibility := PermissionInfo{State: "denied", Scope: "macos.accessibility", Detail: "Accessibility access is not granted"}
	if C.ext_system_ax_trusted() != 0 {
		accessibility = PermissionInfo{State: "ready", Scope: "macos.accessibility"}
	}
	recording := PermissionInfo{State: "denied", Scope: "macos.screen-recording", Detail: "Screen Recording access is not granted"}
	if C.ext_system_screen_recording() != 0 {
		recording = PermissionInfo{State: "ready", Scope: "macos.screen-recording"}
	}
	return accessibility, recording, nil
}
