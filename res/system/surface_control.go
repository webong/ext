package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrSurfaceUnavailable means the native desktop cannot perform the
	// requested operation in the current session.
	ErrSurfaceUnavailable = errors.New("surface operation unavailable")
	// ErrSurfacePermission means the operating system has not granted the
	// calling process permission for the requested operation.
	ErrSurfacePermission = errors.New("surface operation permission is not granted")
	// ErrSurfaceStale means the observed process generation or window no longer
	// names the same native surface.
	ErrSurfaceStale = errors.New("surface reference is stale")
	// ErrSurfaceNotFocused means input could not be safely directed to the
	// selected surface without affecting another application.
	ErrSurfaceNotFocused = errors.New("surface is not focused")
)

// SurfaceCapture is an encoded image captured from one observed window. Data
// contains the image bytes directly; transport encodings belong to callers.
type SurfaceCapture struct {
	Format     string    `json:"format"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	Data       []byte    `json:"data"`
	CapturedAt time.Time `json:"captured_at"`
}

// CaptureOptions bounds native allocation and encoded output. Zero values
// select 16,777,216 pixels, 16 MiB, and a five-second deadline.
type CaptureOptions struct {
	MaxPixels int
	MaxBytes  int
	Timeout   time.Duration
}

func captureOptions(options CaptureOptions) (CaptureOptions, error) {
	if options.MaxPixels < 0 || options.MaxBytes < 0 || options.Timeout < 0 {
		return options, errors.New("capture limits and timeout must not be negative")
	}
	if options.MaxPixels == 0 {
		options.MaxPixels = 16_777_216
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 16 << 20
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.MaxPixels > 268_435_456 || options.MaxBytes > 1<<30 {
		return options, errors.New("capture limits exceed library ceilings")
	}
	return options, nil
}

// CaptureWindow captures a currently observed window after revalidating its
// opaque ID and process generation. It never prompts for permission, selects a
// target, persists the image, or applies caller policy.
func CaptureWindow(ctx context.Context, target WindowInfo, options CaptureOptions) (SurfaceCapture, error) {
	if err := validateSurfaceTarget(target); err != nil {
		return SurfaceCapture{}, err
	}
	options, err := captureOptions(options)
	if err != nil {
		return SurfaceCapture{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	result, err := platformCaptureWindow(ctx, target, options)
	if err != nil {
		return SurfaceCapture{}, err
	}
	if result.Width <= 0 || result.Height <= 0 || len(result.Data) == 0 || result.Format == "" {
		return SurfaceCapture{}, errors.New("native capture returned an invalid image")
	}
	if result.Width > options.MaxPixels/result.Height || len(result.Data) > options.MaxBytes {
		return SurfaceCapture{}, errors.New("native capture exceeded configured limits")
	}
	if result.CapturedAt.IsZero() {
		result.CapturedAt = time.Now().UTC()
	}
	return result, nil
}

// SurfaceInputKind identifies one bounded, atomic native input operation.
type SurfaceInputKind string

const (
	SurfaceInputMove   SurfaceInputKind = "pointer-move"
	SurfaceInputClick  SurfaceInputKind = "pointer-click"
	SurfaceInputDrag   SurfaceInputKind = "pointer-drag"
	SurfaceInputScroll SurfaceInputKind = "pointer-scroll"
	SurfaceInputText   SurfaceInputKind = "keyboard-text"
	SurfaceInputKey    SurfaceInputKind = "keyboard-key"
)

// SurfacePoint uses window-local coordinates in the current window bounds.
type SurfacePoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// SurfaceInput describes one atomic operation. Point is used by pointer
// operations, End by drag, DeltaX/DeltaY by scroll, Text by keyboard-text, and
// Key/Modifiers by keyboard-key.
type SurfaceInput struct {
	Kind      SurfaceInputKind `json:"kind"`
	Point     SurfacePoint     `json:"point,omitempty"`
	End       SurfacePoint     `json:"end,omitempty"`
	Button    string           `json:"button,omitempty"`
	DeltaX    int              `json:"delta_x,omitempty"`
	DeltaY    int              `json:"delta_y,omitempty"`
	Text      string           `json:"text,omitempty"`
	Key       string           `json:"key,omitempty"`
	Modifiers []string         `json:"modifiers,omitempty"`
}

// InputOptions bounds one atomic native input operation. Zero values select a
// 4096-rune text ceiling and a five-second deadline.
type InputOptions struct {
	MaxTextRunes int
	Timeout      time.Duration
}

func inputOptions(options InputOptions) (InputOptions, error) {
	if options.MaxTextRunes < 0 || options.Timeout < 0 {
		return options, errors.New("input limits and timeout must not be negative")
	}
	if options.MaxTextRunes == 0 {
		options.MaxTextRunes = 4096
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.MaxTextRunes > 1_000_000 {
		return options, errors.New("input text limit exceeds library ceiling")
	}
	return options, nil
}

// SendWindowInput sends one bounded operation to a revalidated local window.
// It does not choose or activate the window, hold input across calls, implement
// an approval policy, or expose a remote protocol.
func SendWindowInput(ctx context.Context, target WindowInfo, input SurfaceInput, options InputOptions) error {
	if err := validateSurfaceTarget(target); err != nil {
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
	if err := platformSendWindowInput(ctx, target, input); err != nil {
		return err
	}
	return nil
}

func validateSurfaceTarget(target WindowInfo) error {
	if !strings.HasPrefix(target.ID, "window:") || target.PID <= 0 || target.ProcessStartID == "" {
		return errors.New("surface target requires an observed window ID and process generation")
	}
	if target.Bounds.Width <= 0 || target.Bounds.Height <= 0 {
		return errors.New("surface target requires positive bounds")
	}
	return nil
}

func validateSurfaceInput(input SurfaceInput, options InputOptions) (SurfaceInput, error) {
	point := func(value SurfacePoint) error {
		if value.X < 0 || value.Y < 0 {
			return errors.New("surface coordinates must not be negative")
		}
		return nil
	}
	switch input.Kind {
	case SurfaceInputMove:
		if err := point(input.Point); err != nil {
			return input, err
		}
	case SurfaceInputClick:
		if err := point(input.Point); err != nil {
			return input, err
		}
		input.Button = strings.ToLower(strings.TrimSpace(input.Button))
		if input.Button == "" {
			input.Button = "left"
		}
		if input.Button != "left" && input.Button != "right" && input.Button != "middle" {
			return input, errors.New("unsupported pointer button")
		}
	case SurfaceInputDrag:
		if err := point(input.Point); err != nil {
			return input, err
		}
		if err := point(input.End); err != nil {
			return input, err
		}
	case SurfaceInputScroll:
		if err := point(input.Point); err != nil {
			return input, err
		}
		if input.DeltaX < -1_000_000 || input.DeltaX > 1_000_000 || input.DeltaY < -1_000_000 || input.DeltaY > 1_000_000 {
			return input, errors.New("scroll delta exceeds library bounds")
		}
	case SurfaceInputText:
		if input.Text == "" {
			return input, errors.New("keyboard text must not be empty")
		}
		if len([]rune(input.Text)) > options.MaxTextRunes {
			return input, errors.New("keyboard text exceeds configured limit")
		}
	case SurfaceInputKey:
		input.Key = strings.ToLower(strings.TrimSpace(input.Key))
		if input.Key == "" {
			return input, errors.New("keyboard key must not be empty")
		}
		input.Modifiers = append([]string(nil), input.Modifiers...)
		seen := map[string]bool{}
		for index, modifier := range input.Modifiers {
			modifier = strings.ToLower(strings.TrimSpace(modifier))
			if modifier != "command" && modifier != "control" && modifier != "alt" && modifier != "shift" {
				return input, fmt.Errorf("unsupported keyboard modifier %q", modifier)
			}
			if seen[modifier] {
				return input, fmt.Errorf("duplicate keyboard modifier %q", modifier)
			}
			seen[modifier] = true
			input.Modifiers[index] = modifier
		}
	default:
		return input, fmt.Errorf("unsupported surface input kind %q", input.Kind)
	}
	return input, nil
}
