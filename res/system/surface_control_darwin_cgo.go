//go:build darwin && cgo

package system

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation -framework CoreGraphics -framework ImageIO
#include <ApplicationServices/ApplicationServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CoreGraphics.h>
#include <ImageIO/ImageIO.h>
#include <dlfcn.h>
#include <math.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    uint32_t identifier;
    int32_t pid;
    double x;
    double y;
    double width;
    double height;
} ext_control_window;

static int ext_control_windows(ext_control_window **output, int *written) {
    CFArrayRef list = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (list == NULL) return -1;
    CFIndex count = CFArrayGetCount(list);
    ext_control_window *values = count == 0 ? NULL : calloc((size_t)count, sizeof(ext_control_window));
    if (count != 0 && values == NULL) { CFRelease(list); return -2; }
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
    return 0;
}

static int ext_control_capture(uint32_t identifier, size_t max_bytes,
                               uint8_t **output, size_t *length,
                               size_t *width, size_t *height) {
    if (!CGPreflightScreenCaptureAccess()) return -6;
    typedef CGImageRef (*ext_window_image_fn)(CGRect, CGWindowListOption, CGWindowID, CGWindowImageOption);
    ext_window_image_fn create_image = (ext_window_image_fn)dlsym(RTLD_DEFAULT, "CGWindowListCreateImage");
    if (create_image == NULL) return -5;
    CGImageRef image = create_image(
        CGRectNull, kCGWindowListOptionIncludingWindow, identifier,
        kCGWindowImageBoundsIgnoreFraming | kCGWindowImageBestResolution);
    if (image == NULL) return -1;
    *width = CGImageGetWidth(image);
    *height = CGImageGetHeight(image);
    CFMutableDataRef data = CFDataCreateMutable(kCFAllocatorDefault, 0);
    if (data == NULL) { CGImageRelease(image); return -2; }
    CGImageDestinationRef destination = CGImageDestinationCreateWithData(
        data, CFSTR("public.png"), 1, NULL);
    if (destination == NULL) { CFRelease(data); CGImageRelease(image); return -2; }
    CGImageDestinationAddImage(destination, image, NULL);
    bool encoded = CGImageDestinationFinalize(destination);
    CFRelease(destination);
    CGImageRelease(image);
    if (!encoded) { CFRelease(data); return -3; }
    CFIndex count = CFDataGetLength(data);
    if (count <= 0 || (size_t)count > max_bytes) { CFRelease(data); return -4; }
    uint8_t *bytes = malloc((size_t)count);
    if (bytes == NULL) { CFRelease(data); return -2; }
    memcpy(bytes, CFDataGetBytePtr(data), (size_t)count);
    CFRelease(data);
    *output = bytes;
    *length = (size_t)count;
    return 0;
}

static bool ext_control_copy_point(AXUIElementRef element, CFStringRef name, CGPoint *point) {
    CFTypeRef value = NULL;
    bool ok = AXUIElementCopyAttributeValue(element, name, &value) == kAXErrorSuccess &&
        value != NULL && CFGetTypeID(value) == AXValueGetTypeID() &&
        AXValueGetValue((AXValueRef)value, kAXValueCGPointType, point);
    if (value != NULL) CFRelease(value);
    return ok;
}

static bool ext_control_copy_size(AXUIElementRef element, CFStringRef name, CGSize *size) {
    CFTypeRef value = NULL;
    bool ok = AXUIElementCopyAttributeValue(element, name, &value) == kAXErrorSuccess &&
        value != NULL && CFGetTypeID(value) == AXValueGetTypeID() &&
        AXValueGetValue((AXValueRef)value, kAXValueCGSizeType, size);
    if (value != NULL) CFRelease(value);
    return ok;
}

static int ext_control_focused(int32_t pid, double x, double y, double width, double height) {
    if (!AXIsProcessTrusted()) return -1;
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    if (app == NULL) return -2;
    AXUIElementSetMessagingTimeout(app, 0.25);
    CFTypeRef value = NULL;
    AXError error = AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, &value);
    CFRelease(app);
    if (error != kAXErrorSuccess || value == NULL || CFGetTypeID(value) != AXUIElementGetTypeID()) {
        if (value != NULL) CFRelease(value);
        return -3;
    }
    AXUIElementRef window = (AXUIElementRef)value;
    CGPoint position = CGPointZero;
    CGSize size = CGSizeZero;
    bool ok = ext_control_copy_point(window, kAXPositionAttribute, &position) &&
        ext_control_copy_size(window, kAXSizeAttribute, &size);
    CFRelease(window);
    if (!ok) return -3;
    return fabs(position.x - x) < 1.0 && fabs(position.y - y) < 1.0 &&
        fabs(size.width - width) < 1.0 && fabs(size.height - height) < 1.0 ? 0 : -4;
}

static CGMouseButton ext_control_button(int button) {
    return button == 1 ? kCGMouseButtonRight : button == 2 ? kCGMouseButtonCenter : kCGMouseButtonLeft;
}

static int ext_control_pointer(int32_t pid, int operation, int button,
                               double x, double y, double end_x, double end_y,
                               int32_t delta_x, int32_t delta_y) {
    CGMouseButton resolved = ext_control_button(button);
    CGPoint start = CGPointMake(x, y);
    if (operation == 0) {
        CGEventRef event = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, start, resolved);
        if (event == NULL) return -1;
        CGEventPostToPid(pid, event); CFRelease(event); return 0;
    }
    if (operation == 3) {
        CGEventRef event = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitPixel, 2, delta_y, delta_x);
        if (event == NULL) return -1;
        CGEventSetLocation(event, start);
        CGEventPostToPid(pid, event); CFRelease(event); return 0;
    }
    CGEventType down_type = resolved == kCGMouseButtonLeft ? kCGEventLeftMouseDown :
        resolved == kCGMouseButtonRight ? kCGEventRightMouseDown : kCGEventOtherMouseDown;
    CGEventType up_type = resolved == kCGMouseButtonLeft ? kCGEventLeftMouseUp :
        resolved == kCGMouseButtonRight ? kCGEventRightMouseUp : kCGEventOtherMouseUp;
    CGEventRef down = CGEventCreateMouseEvent(NULL, down_type, start, resolved);
    CGEventRef up = CGEventCreateMouseEvent(NULL, up_type,
        operation == 2 ? CGPointMake(end_x, end_y) : start, resolved);
    if (down == NULL || up == NULL) {
        if (down != NULL) CFRelease(down); if (up != NULL) CFRelease(up); return -1;
    }
    CGEventPostToPid(pid, down);
    if (operation == 2) {
        CGEventType drag_type = resolved == kCGMouseButtonLeft ? kCGEventLeftMouseDragged :
            resolved == kCGMouseButtonRight ? kCGEventRightMouseDragged : kCGEventOtherMouseDragged;
        CGEventRef drag = CGEventCreateMouseEvent(NULL, drag_type, CGPointMake(end_x, end_y), resolved);
        if (drag == NULL) { CGEventPostToPid(pid, up); CFRelease(down); CFRelease(up); return -1; }
        CGEventPostToPid(pid, drag); CFRelease(drag);
    }
    CGEventPostToPid(pid, up);
    CFRelease(down); CFRelease(up); return 0;
}

static int ext_control_text(int32_t pid, const uint16_t *characters, size_t count) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, 0, false);
    if (down == NULL || up == NULL) {
        if (down != NULL) CFRelease(down); if (up != NULL) CFRelease(up); return -1;
    }
    CGEventKeyboardSetUnicodeString(down, count, characters);
    CGEventKeyboardSetUnicodeString(up, count, characters);
    CGEventPostToPid(pid, down); CGEventPostToPid(pid, up);
    CFRelease(down); CFRelease(up); return 0;
}

static int ext_control_key(int32_t pid, uint16_t code, uint64_t flags) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, code, true);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, code, false);
    if (down == NULL || up == NULL) {
        if (down != NULL) CFRelease(down); if (up != NULL) CFRelease(up); return -1;
    }
    CGEventSetFlags(down, flags); CGEventSetFlags(up, flags);
    CGEventPostToPid(pid, down); CGEventPostToPid(pid, up);
    CFRelease(down); CFRelease(up); return 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf16"
	"unsafe"
)

type controlDarwinSurface struct {
	identifier uint32
	pid        int
	start      string
	bounds     SurfaceBounds
}

func platformCaptureWindow(ctx context.Context, target WindowInfo, options CaptureOptions) (SurfaceCapture, error) {
	surface, err := resolveDarwinSurface(ctx, target)
	if err != nil {
		return SurfaceCapture{}, err
	}
	if surface.bounds.Width > options.MaxPixels/surface.bounds.Height {
		return SurfaceCapture{}, errors.New("surface exceeds capture pixel limit")
	}
	var output *C.uint8_t
	var length, width, height C.size_t
	code := C.ext_control_capture(C.uint32_t(surface.identifier), C.size_t(options.MaxBytes), &output, &length, &width, &height)
	if output != nil {
		defer C.free(unsafe.Pointer(output))
	}
	switch code {
	case 0:
	case -1:
		return SurfaceCapture{}, fmt.Errorf("%w: Core Graphics capture failed", ErrSurfaceUnavailable)
	case -4:
		return SurfaceCapture{}, errors.New("encoded capture exceeds byte limit")
	case -5:
		return SurfaceCapture{}, fmt.Errorf("%w: this macOS release requires the ExtSystem Swift capture implementation", ErrSurfaceUnavailable)
	case -6:
		return SurfaceCapture{}, ErrSurfacePermission
	default:
		return SurfaceCapture{}, fmt.Errorf("native surface capture failed (%d)", int(code))
	}
	if int(width) <= 0 || int(height) <= 0 || int(width) > options.MaxPixels/int(height) {
		return SurfaceCapture{}, errors.New("captured image exceeds pixel limit")
	}
	return SurfaceCapture{Format: "image/png", Width: int(width), Height: int(height), Data: C.GoBytes(unsafe.Pointer(output), C.int(length))}, nil
}

func platformSendWindowInput(ctx context.Context, target WindowInfo, input SurfaceInput) error {
	surface, err := resolveDarwinSurface(ctx, target)
	if err != nil {
		return err
	}
	focused := C.ext_control_focused(C.int32_t(surface.pid), C.double(surface.bounds.X), C.double(surface.bounds.Y),
		C.double(surface.bounds.Width), C.double(surface.bounds.Height))
	if focused == -1 {
		return ErrSurfacePermission
	}
	if focused != 0 {
		return ErrSurfaceNotFocused
	}
	point := func(value SurfacePoint) (float64, float64, error) {
		if value.X < 0 || value.Y < 0 || value.X >= surface.bounds.Width || value.Y >= surface.bounds.Height {
			return 0, 0, errors.New("surface coordinates exceed current window bounds")
		}
		return float64(surface.bounds.X + value.X), float64(surface.bounds.Y + value.Y), nil
	}
	switch input.Kind {
	case SurfaceInputMove, SurfaceInputClick, SurfaceInputDrag, SurfaceInputScroll:
		x, y, err := point(input.Point)
		if err != nil {
			return err
		}
		endX, endY := x, y
		if input.Kind == SurfaceInputDrag {
			endX, endY, err = point(input.End)
			if err != nil {
				return err
			}
		}
		operation := map[SurfaceInputKind]int{SurfaceInputMove: 0, SurfaceInputClick: 1, SurfaceInputDrag: 2, SurfaceInputScroll: 3}[input.Kind]
		button := map[string]int{"left": 0, "right": 1, "middle": 2}[input.Button]
		if code := C.ext_control_pointer(C.int32_t(surface.pid), C.int(operation), C.int(button), C.double(x), C.double(y),
			C.double(endX), C.double(endY), C.int32_t(input.DeltaX), C.int32_t(input.DeltaY)); code != 0 {
			return fmt.Errorf("%w: Core Graphics pointer event", ErrSurfaceUnavailable)
		}
		return nil
	case SurfaceInputText:
		characters := utf16.Encode([]rune(input.Text))
		if code := C.ext_control_text(C.int32_t(surface.pid), (*C.uint16_t)(unsafe.Pointer(&characters[0])), C.size_t(len(characters))); code != 0 {
			return fmt.Errorf("%w: Core Graphics keyboard event", ErrSurfaceUnavailable)
		}
		return nil
	case SurfaceInputKey:
		key, ok := darwinKeyCode(input.Key)
		if !ok {
			return errors.New("unsupported keyboard key")
		}
		var flags uint64
		for _, modifier := range input.Modifiers {
			flags |= map[string]uint64{
				"command": 1 << 20, "control": 1 << 18, "alt": 1 << 19, "shift": 1 << 17,
			}[modifier]
		}
		if code := C.ext_control_key(C.int32_t(surface.pid), C.uint16_t(key), C.uint64_t(flags)); code != 0 {
			return fmt.Errorf("%w: Core Graphics keyboard event", ErrSurfaceUnavailable)
		}
		return nil
	default:
		return errors.New("unsupported surface input")
	}
}

func resolveDarwinSurface(ctx context.Context, target WindowInfo) (controlDarwinSurface, error) {
	var values *C.ext_control_window
	var written C.int
	code := C.ext_control_windows(&values, &written)
	if values != nil {
		defer C.free(unsafe.Pointer(values))
	}
	if code != 0 {
		return controlDarwinSurface{}, fmt.Errorf("%w: Core Graphics window enumeration failed", ErrSurfaceUnavailable)
	}
	starts := map[int]string{}
	for _, value := range unsafe.Slice(values, int(written)) {
		if err := ctx.Err(); err != nil {
			return controlDarwinSurface{}, err
		}
		pid := int(value.pid)
		start, exists := starts[pid]
		if !exists {
			process, err := platformProcess(ctx, pid)
			if err == nil {
				start = process.StartID
			}
			starts[pid] = start
		}
		native := strconv.FormatUint(uint64(value.identifier), 10)
		if start == "" || surfaceWindowID(pid, start, native) != target.ID {
			continue
		}
		if pid != target.PID || start != target.ProcessStartID {
			return controlDarwinSurface{}, ErrSurfaceStale
		}
		bounds := SurfaceBounds{X: int(value.x), Y: int(value.y), Width: int(value.width), Height: int(value.height)}
		if bounds.Width <= 0 || bounds.Height <= 0 {
			return controlDarwinSurface{}, ErrSurfaceStale
		}
		return controlDarwinSurface{identifier: uint32(value.identifier), pid: pid, start: start, bounds: bounds}, nil
	}
	return controlDarwinSurface{}, ErrSurfaceStale
}

func darwinKeyCode(key string) (uint16, bool) {
	keys := map[string]uint16{
		"enter": 36, "tab": 48, "escape": 53, "space": 49,
		"left": 123, "right": 124, "down": 125, "up": 126,
		"backspace": 51, "delete": 117,
	}
	value, ok := keys[key]
	return value, ok
}
