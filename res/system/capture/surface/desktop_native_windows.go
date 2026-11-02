//go:build windows

package surface

import (
	"context"
	"errors"
	"fmt"
)

var controlGetSystemMetrics = controlUser32.NewProc("GetSystemMetrics")

const (
	windowsVirtualScreenX      = 76
	windowsVirtualScreenY      = 77
	windowsVirtualScreenWidth  = 78
	windowsVirtualScreenHeight = 79
)

// windowsDesktop drives the whole virtual screen with GDI and SendInput.
type windowsDesktop struct{}

func newNativeDesktopBackend() (nativeDesktopBackend, error) { return windowsDesktop{}, nil }

func (windowsDesktop) name() string { return "native-windows" }

func (windowsDesktop) bounds(context.Context) (SurfaceBounds, error) {
	metric := func(index uintptr) int {
		value, _, _ := controlGetSystemMetrics.Call(index)
		return int(int32(value))
	}
	bounds := SurfaceBounds{
		X: metric(windowsVirtualScreenX), Y: metric(windowsVirtualScreenY),
		Width: metric(windowsVirtualScreenWidth), Height: metric(windowsVirtualScreenHeight),
	}
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return SurfaceBounds{}, fmt.Errorf("%w: the virtual screen has no size", ErrSurfaceUnavailable)
	}
	return bounds, nil
}

func (d windowsDesktop) capture(ctx context.Context, options CaptureOptions) (SurfaceCapture, error) {
	bounds, err := d.bounds(ctx)
	if err != nil {
		return SurfaceCapture{}, err
	}
	if bounds.Width > options.MaxPixels/bounds.Height {
		return SurfaceCapture{}, errors.New("desktop exceeds capture pixel limit")
	}
	return captureWindowsRect(ctx, int32(bounds.X), int32(bounds.Y), int32(bounds.Width), int32(bounds.Height), options)
}

func (windowsDesktop) pointer(ctx context.Context, event nativePointer) error {
	absolute := func(point SurfacePoint) error { return setWindowsCursor(point.X, point.Y) }
	at := SurfacePoint{X: event.x, Y: event.y}
	switch event.op {
	case pointerMove:
		return sendWindowsInput(ctx, SurfaceInput{Kind: SurfaceInputMove, Point: at}, absolute)
	case pointerClick:
		return sendWindowsInput(ctx, SurfaceInput{Kind: SurfaceInputClick, Point: at, Button: event.button}, absolute)
	case pointerDrag:
		end := SurfacePoint{X: event.endX, Y: event.endY}
		return sendWindowsInput(ctx, SurfaceInput{Kind: SurfaceInputDrag, Point: at, End: end}, absolute)
	case pointerScroll:
		return sendWindowsInput(ctx, SurfaceInput{Kind: SurfaceInputScroll, Point: at, DeltaX: event.deltaX, DeltaY: event.deltaY}, absolute)
	case pointerDown, pointerUp:
		if err := absolute(at); err != nil {
			return err
		}
		down, up := windowsMouseButton(event.button)
		flag := down
		if event.op == pointerUp {
			flag = up
		}
		controlMouseEvent.Call(flag, 0, 0, 0, 0)
		return nil
	case pointerHeldMove:
		// Windows keeps the button state itself, so moving with a button down is a drag.
		return absolute(at)
	default:
		return errors.New("unsupported pointer operation")
	}
}

func (windowsDesktop) text(ctx context.Context, text string) error {
	return sendWindowsInput(ctx, SurfaceInput{Kind: SurfaceInputText, Text: text}, nil)
}

func (windowsDesktop) key(ctx context.Context, key string, modifiers []string) error {
	return sendWindowsInput(ctx, SurfaceInput{Kind: SurfaceInputKey, Key: key, Modifiers: modifiers}, nil)
}
