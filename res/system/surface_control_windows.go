//go:build windows

package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strconv"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	controlUser32                 = windows.NewLazySystemDLL("user32.dll")
	controlGDI32                  = windows.NewLazySystemDLL("gdi32.dll")
	controlGetForegroundWindow    = controlUser32.NewProc("GetForegroundWindow")
	controlSetCursorPosition      = controlUser32.NewProc("SetCursorPos")
	controlMouseEvent             = controlUser32.NewProc("mouse_event")
	controlSendInput              = controlUser32.NewProc("SendInput")
	controlGetDC                  = controlUser32.NewProc("GetDC")
	controlReleaseDC              = controlUser32.NewProc("ReleaseDC")
	controlCreateCompatibleDC     = controlGDI32.NewProc("CreateCompatibleDC")
	controlDeleteDC               = controlGDI32.NewProc("DeleteDC")
	controlCreateCompatibleBitmap = controlGDI32.NewProc("CreateCompatibleBitmap")
	controlSelectObject           = controlGDI32.NewProc("SelectObject")
	controlDeleteObject           = controlGDI32.NewProc("DeleteObject")
	controlBitBlt                 = controlGDI32.NewProc("BitBlt")
	controlGetDIBits              = controlGDI32.NewProc("GetDIBits")
)

const (
	controlSourceCopy      = 0x00CC0020
	controlDIBRGBColors    = 0
	controlMouseLeftDown   = 0x0002
	controlMouseLeftUp     = 0x0004
	controlMouseRightDown  = 0x0008
	controlMouseRightUp    = 0x0010
	controlMouseMiddleDown = 0x0020
	controlMouseMiddleUp   = 0x0040
	controlMouseWheel      = 0x0800
	controlMouseHorizontal = 0x1000
	controlInputKeyboard   = 1
	controlKeyUp           = 0x0002
	controlKeyUnicode      = 0x0004
)

type controlWindowsBitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ColorsUsed, ColorsImportant  uint32
}

type controlWindowsBitmapInfo struct {
	Header controlWindowsBitmapInfoHeader
	Colors [1]uint32
}

type controlWindowsSurface struct {
	handle uintptr
	pid    int
	start  string
	bounds surfaceWindowsRect
}

func platformCaptureWindow(ctx context.Context, target WindowInfo, options CaptureOptions) (SurfaceCapture, error) {
	surface, err := resolveWindowsSurface(ctx, target)
	if err != nil {
		return SurfaceCapture{}, err
	}
	width, height := surface.bounds.Right-surface.bounds.Left, surface.bounds.Bottom-surface.bounds.Top
	if width <= 0 || height <= 0 || int(width) > options.MaxPixels/int(height) {
		return SurfaceCapture{}, errors.New("surface exceeds capture pixel limit")
	}
	screenDC, _, _ := controlGetDC.Call(0)
	if screenDC == 0 {
		return SurfaceCapture{}, fmt.Errorf("%w: GetDC", ErrSurfaceUnavailable)
	}
	defer controlReleaseDC.Call(0, screenDC)
	memoryDC, _, _ := controlCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return SurfaceCapture{}, fmt.Errorf("%w: CreateCompatibleDC", ErrSurfaceUnavailable)
	}
	defer controlDeleteDC.Call(memoryDC)
	bitmap, _, _ := controlCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return SurfaceCapture{}, fmt.Errorf("%w: CreateCompatibleBitmap", ErrSurfaceUnavailable)
	}
	defer controlDeleteObject.Call(bitmap)
	previous, _, _ := controlSelectObject.Call(memoryDC, bitmap)
	if previous == 0 {
		return SurfaceCapture{}, fmt.Errorf("%w: SelectObject", ErrSurfaceUnavailable)
	}
	defer controlSelectObject.Call(memoryDC, previous)
	if result, _, _ := controlBitBlt.Call(memoryDC, 0, 0, uintptr(width), uintptr(height), screenDC,
		uintptr(surface.bounds.Left), uintptr(surface.bounds.Top), controlSourceCopy); result == 0 {
		return SurfaceCapture{}, fmt.Errorf("%w: BitBlt", ErrSurfaceUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return SurfaceCapture{}, err
	}
	pixels := make([]byte, int(width)*int(height)*4)
	info := controlWindowsBitmapInfo{Header: controlWindowsBitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(controlWindowsBitmapInfoHeader{})), Width: width, Height: -height,
		Planes: 1, BitCount: 32,
	}}
	if result, _, _ := controlGetDIBits.Call(memoryDC, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&info)), controlDIBRGBColors); result == 0 {
		return SurfaceCapture{}, fmt.Errorf("%w: GetDIBits", ErrSurfaceUnavailable)
	}
	for offset := 0; offset < len(pixels); offset += 4 {
		pixels[offset], pixels[offset+2] = pixels[offset+2], pixels[offset]
		pixels[offset+3] = 0xff // GDI does not define alpha for a BitBlt DIB.
	}
	output := &captureBuffer{limit: options.MaxBytes}
	if err := png.Encode(output, &image.RGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}); err != nil {
		if errors.Is(err, errCaptureTooLarge) {
			return SurfaceCapture{}, err
		}
		return SurfaceCapture{}, fmt.Errorf("encode surface capture: %w", err)
	}
	return SurfaceCapture{Format: "image/png", Width: int(width), Height: int(height), Data: output.Bytes()}, nil
}

func platformSendWindowInput(ctx context.Context, target WindowInfo, input SurfaceInput) error {
	surface, err := resolveWindowsSurface(ctx, target)
	if err != nil {
		return err
	}
	foreground, _, _ := controlGetForegroundWindow.Call()
	if foreground != surface.handle {
		return ErrSurfaceNotFocused
	}
	point := func(value SurfacePoint) error {
		width, height := int(surface.bounds.Right-surface.bounds.Left), int(surface.bounds.Bottom-surface.bounds.Top)
		if value.X < 0 || value.Y < 0 || value.X >= width || value.Y >= height {
			return errors.New("surface coordinates exceed current window bounds")
		}
		result, _, _ := controlSetCursorPosition.Call(uintptr(int(surface.bounds.Left)+value.X), uintptr(int(surface.bounds.Top)+value.Y))
		if result == 0 {
			return fmt.Errorf("%w: SetCursorPos", ErrSurfaceUnavailable)
		}
		return nil
	}
	switch input.Kind {
	case SurfaceInputMove:
		return point(input.Point)
	case SurfaceInputClick:
		if err := point(input.Point); err != nil {
			return err
		}
		down, up := windowsMouseButton(input.Button)
		controlMouseEvent.Call(down, 0, 0, 0, 0)
		controlMouseEvent.Call(up, 0, 0, 0, 0)
		return nil
	case SurfaceInputDrag:
		if err := point(input.Point); err != nil {
			return err
		}
		controlMouseEvent.Call(controlMouseLeftDown, 0, 0, 0, 0)
		if err := point(input.End); err != nil {
			controlMouseEvent.Call(controlMouseLeftUp, 0, 0, 0, 0)
			return err
		}
		controlMouseEvent.Call(controlMouseLeftUp, 0, 0, 0, 0)
		return nil
	case SurfaceInputScroll:
		if err := point(input.Point); err != nil {
			return err
		}
		if input.DeltaY != 0 {
			controlMouseEvent.Call(controlMouseWheel, 0, 0, uintptr(uint32(int32(input.DeltaY*120))), 0)
		}
		if input.DeltaX != 0 {
			controlMouseEvent.Call(controlMouseHorizontal, 0, 0, uintptr(uint32(int32(input.DeltaX*120))), 0)
		}
		return nil
	case SurfaceInputText:
		for _, unit := range utf16.Encode([]rune(input.Text)) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := sendWindowsKey(0, unit, controlKeyUnicode); err != nil {
				return err
			}
			if err := sendWindowsKey(0, unit, controlKeyUnicode|controlKeyUp); err != nil {
				return err
			}
		}
		return nil
	case SurfaceInputKey:
		key, ok := windowsVirtualKey(input.Key)
		if !ok {
			return errors.New("unsupported keyboard key")
		}
		modifiers := make([]uint16, 0, len(input.Modifiers))
		for _, modifier := range input.Modifiers {
			value, _ := windowsVirtualKey(modifier)
			modifiers = append(modifiers, value)
			if err := sendWindowsKey(value, 0, 0); err != nil {
				return err
			}
		}
		if err := sendWindowsKey(key, 0, 0); err != nil {
			return err
		}
		if err := sendWindowsKey(key, 0, controlKeyUp); err != nil {
			return err
		}
		for index := len(modifiers) - 1; index >= 0; index-- {
			if err := sendWindowsKey(modifiers[index], 0, controlKeyUp); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("unsupported surface input")
	}
}

func resolveWindowsSurface(ctx context.Context, target WindowInfo) (controlWindowsSurface, error) {
	var resolved controlWindowsSurface
	var callbackErr error
	found := false
	starts := map[int]string{}
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		if err := ctx.Err(); err != nil {
			callbackErr = err
			return 0
		}
		visible, _, _ := surfaceIsWindowVisible.Call(window)
		if visible == 0 {
			return 1
		}
		var bounds surfaceWindowsRect
		ok, _, _ := surfaceGetWindowRect.Call(window, uintptr(unsafe.Pointer(&bounds)))
		if ok == 0 || bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top {
			return 1
		}
		var processID uint32
		surfaceGetWindowThreadProcess.Call(window, uintptr(unsafe.Pointer(&processID)))
		if processID == 0 {
			return 1
		}
		pid := int(processID)
		start, exists := starts[pid]
		if !exists {
			process, err := platformProcess(ctx, pid)
			if err == nil {
				start = process.StartID
			}
			starts[pid] = start
		}
		if start == "" || surfaceWindowID(pid, start, strconv.FormatUint(uint64(window), 10)) != target.ID {
			return 1
		}
		if pid != target.PID || start != target.ProcessStartID {
			callbackErr = ErrSurfaceStale
			return 0
		}
		resolved = controlWindowsSurface{handle: window, pid: pid, start: start, bounds: bounds}
		found = true
		return 0
	})
	surfaceEnumWindows.Call(callback, 0)
	if callbackErr != nil {
		return controlWindowsSurface{}, callbackErr
	}
	if !found {
		return controlWindowsSurface{}, ErrSurfaceStale
	}
	return resolved, nil
}

func windowsMouseButton(button string) (uintptr, uintptr) {
	switch button {
	case "right":
		return controlMouseRightDown, controlMouseRightUp
	case "middle":
		return controlMouseMiddleDown, controlMouseMiddleUp
	default:
		return controlMouseLeftDown, controlMouseLeftUp
	}
}

func windowsVirtualKey(key string) (uint16, bool) {
	keys := map[string]uint16{
		"enter": 0x0d, "tab": 0x09, "escape": 0x1b, "space": 0x20,
		"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
		"backspace": 0x08, "delete": 0x2e,
		"command": 0x5b, "control": 0x11, "alt": 0x12, "shift": 0x10,
	}
	value, ok := keys[key]
	return value, ok
}

func sendWindowsKey(virtualKey, scan uint16, flags uint32) error {
	value := controlWindowsInput{Type: controlInputKeyboard}
	value.Payload.VirtualKey = virtualKey
	value.Payload.Scan = scan
	value.Payload.Flags = flags
	result, _, callErr := controlSendInput.Call(1, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	if result != 1 {
		return fmt.Errorf("%w: SendInput: %v", ErrSurfaceUnavailable, callErr)
	}
	return nil
}

var errCaptureTooLarge = errors.New("encoded capture exceeds byte limit")

type captureBuffer struct {
	bytes.Buffer
	limit int
}

func (w *captureBuffer) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, errCaptureTooLarge
	}
	return w.Buffer.Write(p)
}
