//go:build (!darwin && !windows) || (darwin && !cgo)

package system

import "context"

func platformCaptureWindow(context.Context, WindowInfo, CaptureOptions) (SurfaceCapture, error) {
	return SurfaceCapture{}, ErrSurfaceUnavailable
}

func platformSendWindowInput(context.Context, WindowInfo, SurfaceInput) error {
	return ErrSurfaceUnavailable
}
