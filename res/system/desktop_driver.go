package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/webong/ext/res/system/media"
)

// ErrDesktopUnavailable means a selected desktop mechanism cannot perform the
// requested operation in the current session.
var ErrDesktopUnavailable = errors.New("desktop operation unavailable")

// DesktopDriver is a caller-selected native desktop mechanism, such as the X11
// or Wayland tool adapters. Operations are desktop-scoped because those tools
// cannot address an observed window; window-scoped capture and input remain in
// CaptureWindow and SendWindowInput.
//
// It is an interface rather than a registry: the shared library never selects a
// driver by name or falls back to another one.
type DesktopDriver interface {
	Name() string
	Capture(context.Context, CaptureOptions) (SurfaceCapture, error)
	Input(context.Context, SurfaceInput, InputOptions) error
	Record(context.Context, media.RecordingOptions) (media.Recording, error)
}

// CaptureDesktop captures the caller's desktop through an explicitly selected
// driver. Bounds and result validation are the same as CaptureWindow.
func CaptureDesktop(ctx context.Context, driver DesktopDriver, options CaptureOptions) (SurfaceCapture, error) {
	if err := checkDesktopDriver(driver); err != nil {
		return SurfaceCapture{}, err
	}
	options, err := captureOptions(options)
	if err != nil {
		return SurfaceCapture{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	result, err := driver.Capture(ctx, options)
	if err != nil {
		return SurfaceCapture{}, err
	}
	if result.Width <= 0 || result.Height <= 0 || len(result.Data) == 0 || result.Format == "" {
		return SurfaceCapture{}, errors.New("desktop capture returned an invalid image")
	}
	if result.Width > options.MaxPixels/result.Height || len(result.Data) > options.MaxBytes {
		return SurfaceCapture{}, errors.New("desktop capture exceeded configured limits")
	}
	if result.CapturedAt.IsZero() {
		result.CapturedAt = time.Now().UTC()
	}
	return result, nil
}

// SendDesktopInput sends one bounded operation through the selected driver.
// Drivers that only implement keyboard input must refuse pointer operations
// explicitly instead of emulating them.
func SendDesktopInput(ctx context.Context, driver DesktopDriver, input SurfaceInput, options InputOptions) error {
	if err := checkDesktopDriver(driver); err != nil {
		return err
	}
	options, err := inputOptions(options)
	if err != nil {
		return err
	}
	input, err = validateSurfaceInput(input, options)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	return driver.Input(ctx, input, options)
}

// RecordDesktop starts a bounded recording through the selected driver.
func RecordDesktop(ctx context.Context, driver DesktopDriver, options media.RecordingOptions) (media.Recording, error) {
	if err := checkDesktopDriver(driver); err != nil {
		return nil, err
	}
	options, err := options.Normalize()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRecording, err)
	}
	recording, err := driver.Record(ctx, options)
	if err != nil {
		return nil, err
	}
	if recording == nil {
		return nil, errors.New("desktop recorder returned no stream")
	}
	return recording, nil
}

// ErrInvalidRecording reports malformed recording options.
var ErrInvalidRecording = errors.New("invalid recording options")

func checkDesktopDriver(driver DesktopDriver) error {
	if driver == nil || strings.TrimSpace(driver.Name()) == "" {
		return fmt.Errorf("%w: a named desktop driver is required", ErrDesktopUnavailable)
	}
	return nil
}
