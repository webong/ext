package surface

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/webong/ext/res/system/capture/media"
)

// ErrPointerSessionClosed means a pointer session has ended, because it was closed, ran out
// of time or its context ended, and its held buttons have been released.
var ErrPointerSessionClosed = errors.New("pointer session is closed")

// NativeDesktop is a DesktopDriver for the whole desktop of the current macOS or Windows
// session. It uses the operating system's own capture and event APIs, not an external tool,
// so it is a platform implementation of the surface contract and not an adapter.
//
// Desktop coordinates start at the top-left corner of all displays combined, so every
// position is non-negative and every display is addressable. On macOS one pixel of a capture
// is one point of input. On Windows coordinates are pixels, and the process should be
// DPI-aware so that Windows does not scale them.
//
// Recording needs an encoder, which is a tool; the caller supplies one with
// NativeDesktopOptions.Recorder (adapters/ffmpeg is one). Without it Record is refused.
type NativeDesktop struct {
	backend  nativeDesktopBackend
	recorder media.Recorder
}

// NativeDesktopOptions configures NewNativeDesktop.
type NativeDesktopOptions struct {
	// Recorder produces recordings for Record. It may be nil.
	Recorder media.Recorder
}

// NewNativeDesktop returns the native desktop driver, or an error wrapping ErrDesktopUnavailable
// where the platform has none (anything but macOS built with cgo, and Windows). The caller
// selects it explicitly; the library never falls back to it.
func NewNativeDesktop(options NativeDesktopOptions) (*NativeDesktop, error) {
	backend, err := newNativeDesktopBackend()
	if err != nil {
		return nil, err
	}
	return &NativeDesktop{backend: backend, recorder: options.Recorder}, nil
}

// Name identifies the driver for diagnostics: "native-macos" or "native-windows".
func (d *NativeDesktop) Name() string { return d.backend.name() }

// Capture captures every display as one PNG image.
func (d *NativeDesktop) Capture(ctx context.Context, options CaptureOptions) (SurfaceCapture, error) {
	options, err := captureOptions(options)
	if err != nil {
		return SurfaceCapture{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	result, err := d.backend.capture(ctx, options)
	if err != nil {
		return SurfaceCapture{}, err
	}
	if result.Width <= 0 || result.Height <= 0 || len(result.Data) == 0 || result.Format == "" {
		return SurfaceCapture{}, errors.New("native desktop capture returned an invalid image")
	}
	if result.Width > options.MaxPixels/result.Height || len(result.Data) > options.MaxBytes {
		return SurfaceCapture{}, errors.New("native desktop capture exceeded configured limits")
	}
	if result.CapturedAt.IsZero() {
		result.CapturedAt = time.Now().UTC()
	}
	return result, nil
}

// Input sends one atomic operation to the desktop. Pointer positions must be inside the
// displays. The driver holds nothing across calls; see OpenPointerSession for held buttons.
func (d *NativeDesktop) Input(ctx context.Context, input SurfaceInput, options InputOptions) error {
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
	switch input.Kind {
	case SurfaceInputText:
		return d.backend.text(ctx, input.Text)
	case SurfaceInputKey:
		return d.backend.key(ctx, input.Key, input.Modifiers)
	}
	bounds, err := d.backend.bounds(ctx)
	if err != nil {
		return err
	}
	x, y, err := desktopPoint(bounds, input.Point)
	if err != nil {
		return err
	}
	event := nativePointer{x: x, y: y, button: input.Button, deltaX: input.DeltaX, deltaY: input.DeltaY}
	switch input.Kind {
	case SurfaceInputMove:
		event.op = pointerMove
	case SurfaceInputClick:
		event.op = pointerClick
	case SurfaceInputScroll:
		event.op = pointerScroll
	case SurfaceInputDrag:
		event.op = pointerDrag
		if event.endX, event.endY, err = desktopPoint(bounds, input.End); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported surface input kind %q", input.Kind)
	}
	return d.backend.pointer(ctx, event)
}

// Record starts a recording through the recorder supplied in NativeDesktopOptions.
func (d *NativeDesktop) Record(ctx context.Context, options media.RecordingOptions) (media.Recording, error) {
	if d.recorder == nil {
		return nil, fmt.Errorf("%w: native desktop recording needs a recorder tool; supply NativeDesktopOptions.Recorder", ErrDesktopUnavailable)
	}
	return d.recorder.Record(ctx, options)
}

// PointerSessionOptions bounds one pointer session.
type PointerSessionOptions struct {
	// MaxDuration ends the session, releasing held buttons, after this long. Zero selects
	// five minutes; the ceiling is one hour.
	MaxDuration time.Duration
}

const (
	defaultPointerSession = 5 * time.Minute
	maxPointerSession     = time.Hour
	pointerCallTimeout    = 5 * time.Second
)

// PointerSession holds pointer buttons down across calls, for press-move-release gestures a
// remote viewer needs. Holding is only possible inside a session so that nothing can leave a
// button down: Close, the session's time limit and the end of its context all release every
// held button. A session is safe for concurrent use; calls are serialized.
type PointerSession struct {
	desktop *NativeDesktop

	mu     sync.Mutex
	held   []string
	x, y   int
	closed bool
	timer  *time.Timer
	cancel func() bool
}

// OpenPointerSession starts a pointer session. It ends when Close is called, when
// MaxDuration passes or when ctx is done.
func (d *NativeDesktop) OpenPointerSession(ctx context.Context, options PointerSessionOptions) (*PointerSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.MaxDuration < 0 || options.MaxDuration > maxPointerSession {
		return nil, errors.New("pointer session duration must be between zero and one hour")
	}
	if options.MaxDuration == 0 {
		options.MaxDuration = defaultPointerSession
	}
	session := &PointerSession{desktop: d}
	session.timer = time.AfterFunc(options.MaxDuration, func() { _ = session.Close() })
	session.cancel = context.AfterFunc(ctx, func() { _ = session.Close() })
	return session, nil
}

// Down presses a button at a position and holds it until Up or Close.
func (s *PointerSession) Down(ctx context.Context, point SurfacePoint, button string) error {
	button, err := pointerButton(button)
	if err != nil {
		return err
	}
	return s.do(ctx, point, func(x, y int) error {
		for _, held := range s.held {
			if held == button {
				return fmt.Errorf("pointer button %q is already held", button)
			}
		}
		if err := s.desktop.backend.pointer(ctx, nativePointer{op: pointerDown, button: button, x: x, y: y}); err != nil {
			return err
		}
		s.held = append(s.held, button)
		return nil
	})
}

// Move moves the pointer. While a button is held it drags with that button.
func (s *PointerSession) Move(ctx context.Context, point SurfacePoint) error {
	return s.do(ctx, point, func(x, y int) error {
		event := nativePointer{op: pointerMove, x: x, y: y}
		if len(s.held) > 0 {
			event.op, event.button = pointerHeldMove, s.held[0]
		}
		return s.desktop.backend.pointer(ctx, event)
	})
}

// Up releases a held button at a position.
func (s *PointerSession) Up(ctx context.Context, point SurfacePoint, button string) error {
	button, err := pointerButton(button)
	if err != nil {
		return err
	}
	return s.do(ctx, point, func(x, y int) error {
		index := -1
		for position, held := range s.held {
			if held == button {
				index = position
			}
		}
		if index < 0 {
			return fmt.Errorf("pointer button %q is not held", button)
		}
		if err := s.desktop.backend.pointer(ctx, nativePointer{op: pointerUp, button: button, x: x, y: y}); err != nil {
			return err
		}
		s.held = append(s.held[:index], s.held[index+1:]...)
		return nil
	})
}

// Close ends the session and releases every held button at the last pointer position. It is
// safe to call more than once and after the session has ended by itself.
func (s *PointerSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.timer.Stop()
	s.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), pointerCallTimeout)
	defer cancel()
	var first error
	for index := len(s.held) - 1; index >= 0; index-- {
		if err := s.desktop.backend.pointer(ctx, nativePointer{op: pointerUp, button: s.held[index], x: s.x, y: s.y}); err != nil && first == nil {
			first = err
		}
	}
	s.held = nil
	return first
}

// do runs one pointer call under the session lock after mapping the position into the displays.
func (s *PointerSession) do(ctx context.Context, point SurfacePoint, operation func(x, y int) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrPointerSessionClosed
	}
	ctx, cancel := context.WithTimeout(ctx, pointerCallTimeout)
	defer cancel()
	bounds, err := s.desktop.backend.bounds(ctx)
	if err != nil {
		return err
	}
	x, y, err := desktopPoint(bounds, point)
	if err != nil {
		return err
	}
	if err := operation(x, y); err != nil {
		return err
	}
	s.x, s.y = x, y
	return nil
}

func pointerButton(button string) (string, error) {
	button = strings.ToLower(strings.TrimSpace(button))
	if button == "" {
		button = "left"
	}
	if button != "left" && button != "right" && button != "middle" {
		return "", errors.New("unsupported pointer button")
	}
	return button, nil
}

// desktopPoint maps a position in desktop coordinates to the operating system's global
// coordinates, refusing a position outside the displays.
func desktopPoint(bounds SurfaceBounds, point SurfacePoint) (int, int, error) {
	if point.X < 0 || point.Y < 0 || point.X >= bounds.Width || point.Y >= bounds.Height {
		return 0, 0, errors.New("desktop coordinates exceed the current display bounds")
	}
	return bounds.X + point.X, bounds.Y + point.Y, nil
}

// nativePointerOp is one low-level pointer event.
type nativePointerOp int

const (
	pointerMove nativePointerOp = iota
	pointerClick
	pointerDrag
	pointerScroll
	pointerDown
	pointerUp
	pointerHeldMove
)

// nativePointer is one pointer event in the operating system's global coordinates.
type nativePointer struct {
	op               nativePointerOp
	button           string
	x, y, endX, endY int
	deltaX, deltaY   int
}

// nativeDesktopBackend is the operating-system seam of NativeDesktop. Positions it receives
// are already inside the displays and in global coordinates.
type nativeDesktopBackend interface {
	name() string
	bounds(context.Context) (SurfaceBounds, error)
	capture(context.Context, CaptureOptions) (SurfaceCapture, error)
	pointer(context.Context, nativePointer) error
	text(context.Context, string) error
	key(context.Context, string, []string) error
}
