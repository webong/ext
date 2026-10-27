// Package wayland implements the res/system desktop-driver contract with the
// Wayland command-line tools. It owns grim, wtype, and compositor interaction;
// the caller selects this driver and the bounds.
package wayland

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os/exec"
	"strings"
	"time"

	"github.com/webong/ext/res/system"
	"github.com/webong/ext/res/system/media"
)

type CommandRunner func(context.Context, string, ...string) ([]byte, error)

type Config struct {
	Run     CommandRunner
	Capture string
	Input   string
}

type Driver struct {
	run     CommandRunner
	capture string
	input   string
}

func New(config Config) *Driver {
	if config.Run == nil {
		config.Run = func(ctx context.Context, name string, arguments ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, arguments...).Output()
		}
	}
	if config.Capture == "" {
		config.Capture = "grim"
	}
	if config.Input == "" {
		config.Input = "wtype"
	}
	return &Driver{run: config.Run, capture: config.Capture, input: config.Input}
}

func (d *Driver) Name() string { return "wayland" }

func (d *Driver) Capture(ctx context.Context, options system.CaptureOptions) (system.SurfaceCapture, error) {
	data, err := d.run(ctx, d.capture, "-")
	if err != nil {
		return system.SurfaceCapture{}, fmt.Errorf("%w: grim failed: %v", system.ErrDesktopUnavailable, err)
	}
	if len(data) == 0 || len(data) > options.MaxBytes {
		return system.SurfaceCapture{}, fmt.Errorf("%w: capture bytes exceed the bound", system.ErrDesktopUnavailable)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 {
		return system.SurfaceCapture{}, fmt.Errorf("%w: grim did not return a PNG", system.ErrDesktopUnavailable)
	}
	if config.Width > options.MaxPixels/config.Height {
		return system.SurfaceCapture{}, fmt.Errorf("%w: capture pixels exceed the bound", system.ErrDesktopUnavailable)
	}
	return system.SurfaceCapture{
		Format: "image/png", Width: config.Width, Height: config.Height,
		Data: data, CapturedAt: time.Now().UTC(),
	}, nil
}

func (d *Driver) Input(ctx context.Context, input system.SurfaceInput, options system.InputOptions) error {
	switch input.Kind {
	case system.SurfaceInputText:
		_, err := d.run(ctx, d.input, input.Text)
		return wrapInputError(err)
	case system.SurfaceInputKey:
		arguments := make([]string, 0, len(input.Modifiers)*2+3)
		for _, modifier := range input.Modifiers {
			arguments = append(arguments, "-M", waylandModifier(modifier))
		}
		arguments = append(arguments, "-k", waylandKey(input.Key))
		for _, modifier := range input.Modifiers {
			arguments = append(arguments, "-m", waylandModifier(modifier))
		}
		_, err := d.run(ctx, d.input, arguments...)
		return wrapInputError(err)
	case system.SurfaceInputMove, system.SurfaceInputClick, system.SurfaceInputDrag, system.SurfaceInputScroll:
		return fmt.Errorf("%w: wtype does not provide pointer input", system.ErrDesktopUnavailable)
	}
	return fmt.Errorf("%w: unsupported Wayland input kind %q", system.ErrDesktopUnavailable, input.Kind)
}

// Record is an explicit gap: Wayland recording requires a compositor-specific
// recorder or an XDG desktop portal, and this adapter does not select one.
func (d *Driver) Record(context.Context, media.RecordingOptions) (media.Recording, error) {
	return nil, fmt.Errorf("%w: Wayland recording requires a compositor recorder or portal, which this adapter does not select",
		system.ErrDesktopUnavailable)
}

func waylandModifier(modifier string) string {
	switch modifier {
	case "command":
		return "logo"
	case "control":
		return "ctrl"
	default:
		return modifier
	}
}

func waylandKey(key string) string {
	switch key {
	case "return", "enter":
		return "Return"
	case "tab":
		return "Tab"
	case "escape", "esc":
		return "Escape"
	case "backspace":
		return "BackSpace"
	case "space":
		return "space"
	case "delete":
		return "Delete"
	case "up", "down", "left", "right":
		return strings.ToUpper(key[:1]) + key[1:]
	}
	return key
}

func wrapInputError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: wtype failed: %v", system.ErrDesktopUnavailable, err)
}

var _ system.DesktopDriver = (*Driver)(nil)
