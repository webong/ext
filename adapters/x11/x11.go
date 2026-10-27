// Package x11 implements the res/system desktop-driver contract with the X11
// command-line tools. It owns import, xdotool, and ffmpeg invocation; the caller
// selects this driver, the bounds, and the recording destination.
package x11

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webong/ext/res/system"
	"github.com/webong/ext/res/system/media"
)

type CommandRunner func(context.Context, string, ...string) ([]byte, error)

// Process is the native recording process.
type Process interface {
	Stdout() ioReadCloser
	Wait() error
	Kill() error
}

// ioReadCloser keeps the interface free of an os dependency in tests.
type ioReadCloser interface {
	Read([]byte) (int, error)
	Close() error
}

type Starter func(context.Context, string, ...string) (Process, error)

type Config struct {
	Run        CommandRunner
	Start      Starter
	Screenshot string
	Input      string
	Record     string
}

type Driver struct {
	run        CommandRunner
	start      Starter
	screenshot string
	input      string
	record     string
}

func New(config Config) *Driver {
	if config.Run == nil {
		config.Run = func(ctx context.Context, name string, arguments ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, arguments...).Output()
		}
	}
	if config.Start == nil {
		config.Start = startProcess
	}
	if config.Screenshot == "" {
		config.Screenshot = "import"
	}
	if config.Input == "" {
		config.Input = "xdotool"
	}
	if config.Record == "" {
		config.Record = "ffmpeg"
	}
	return &Driver{
		run: config.Run, start: config.Start,
		screenshot: config.Screenshot, input: config.Input, record: config.Record,
	}
}

func (d *Driver) Name() string { return "x11" }

func (d *Driver) Capture(ctx context.Context, options system.CaptureOptions) (system.SurfaceCapture, error) {
	data, err := d.run(ctx, d.screenshot, "-window", "root", "png:-")
	if err != nil {
		return system.SurfaceCapture{}, fmt.Errorf("%w: import failed: %v", system.ErrDesktopUnavailable, err)
	}
	if len(data) == 0 || len(data) > options.MaxBytes {
		return system.SurfaceCapture{}, fmt.Errorf("%w: capture bytes exceed the bound", system.ErrDesktopUnavailable)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 {
		return system.SurfaceCapture{}, fmt.Errorf("%w: import did not return a PNG", system.ErrDesktopUnavailable)
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
		_, err := d.run(ctx, d.input, "type", "--clearmodifiers", input.Text)
		return wrapInputError(err)
	case system.SurfaceInputKey:
		combo := x11Combo(input)
		_, err := d.run(ctx, d.input, "key", "--clearmodifiers", combo)
		return wrapInputError(err)
	case system.SurfaceInputMove:
		_, err := d.run(ctx, d.input, "mousemove", strconv.Itoa(input.Point.X), strconv.Itoa(input.Point.Y))
		return wrapInputError(err)
	case system.SurfaceInputClick:
		if _, err := d.run(ctx, d.input, "mousemove", strconv.Itoa(input.Point.X), strconv.Itoa(input.Point.Y)); err != nil {
			return wrapInputError(err)
		}
		_, err := d.run(ctx, d.input, "click", strconv.Itoa(x11Button(input.Button)))
		return wrapInputError(err)
	case system.SurfaceInputDrag:
		button := strconv.Itoa(x11Button(input.Button))
		if _, err := d.run(ctx, d.input, "mousemove", strconv.Itoa(input.Point.X), strconv.Itoa(input.Point.Y)); err != nil {
			return wrapInputError(err)
		}
		if _, err := d.run(ctx, d.input, "mousedown", button); err != nil {
			return wrapInputError(err)
		}
		if _, err := d.run(ctx, d.input, "mousemove", strconv.Itoa(input.End.X), strconv.Itoa(input.End.Y)); err != nil {
			return wrapInputError(err)
		}
		_, err := d.run(ctx, d.input, "mouseup", button)
		return wrapInputError(err)
	case system.SurfaceInputScroll:
		if _, err := d.run(ctx, d.input, "mousemove", strconv.Itoa(input.Point.X), strconv.Itoa(input.Point.Y)); err != nil {
			return wrapInputError(err)
		}
		return d.scroll(ctx, input)
	}
	return fmt.Errorf("%w: unsupported X11 input kind %q", system.ErrDesktopUnavailable, input.Kind)
}

func (d *Driver) scroll(ctx context.Context, input system.SurfaceInput) error {
	steps := func(delta int) int {
		value := delta / 120
		if value < 0 {
			value = -value
		}
		if value == 0 {
			value = 1
		}
		if value > 20 {
			value = 20
		}
		return value
	}
	horizontalButton, verticalButton := 6, 5
	if input.DeltaX > 0 {
		horizontalButton = 7
	}
	if input.DeltaY < 0 {
		verticalButton = 4
	}
	count := steps(input.DeltaY)
	if input.DeltaX != 0 {
		count = steps(input.DeltaX)
	}
	for index := 0; index < count; index++ {
		button := verticalButton
		if input.DeltaX != 0 {
			button = horizontalButton
		}
		if _, err := d.run(ctx, d.input, "click", strconv.Itoa(button)); err != nil {
			return wrapInputError(err)
		}
	}
	return nil
}

func (d *Driver) Record(ctx context.Context, options media.RecordingOptions) (media.Recording, error) {
	display := strings.TrimSpace(os.Getenv("DISPLAY"))
	if display == "" {
		return nil, fmt.Errorf("%w: X11 recording requires DISPLAY", system.ErrDesktopUnavailable)
	}
	codec := []string{"-c:v", "libx264", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4"}
	if options.Format == "webm" {
		codec = []string{"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-f", "webm"}
	}
	arguments := []string{
		"-y", "-loglevel", "error", "-f", "x11grab",
		"-framerate", strconv.Itoa(options.FrameRate), "-i", display,
		"-t", strconv.Itoa(int(options.MaxDuration / time.Second)),
		"-fs", strconv.FormatInt(options.MaxBytes, 10),
	}
	arguments = append(arguments, codec...)
	process, err := d.start(ctx, d.record, append(arguments, "pipe:1")...)
	if err != nil {
		return nil, fmt.Errorf("%w: ffmpeg failed to start: %v", system.ErrDesktopUnavailable, err)
	}
	return &recording{process: process, output: process.Stdout()}, nil
}

type recording struct {
	process Process
	output  ioReadCloser
	once    sync.Once
}

func (r *recording) Read(buffer []byte) (int, error) { return r.output.Read(buffer) }

func (r *recording) Close() error { return r.Stop(context.Background()) }

func (r *recording) Stop(ctx context.Context) error {
	var err error
	r.once.Do(func() {
		_ = r.output.Close()
		_ = r.process.Kill()
		done := make(chan error, 1)
		go func() { done <- r.process.Wait() }()
		select {
		case waitErr := <-done:
			err = waitErr
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

func x11Combo(input system.SurfaceInput) string {
	parts := make([]string, 0, len(input.Modifiers)+1)
	for _, modifier := range input.Modifiers {
		switch modifier {
		case "command":
			parts = append(parts, "super")
		default:
			parts = append(parts, modifier)
		}
	}
	parts = append(parts, input.Key)
	return strings.Join(parts, "+")
}

func x11Button(button string) int {
	switch strings.ToLower(button) {
	case "right":
		return 3
	case "middle":
		return 2
	default:
		return 1
	}
}

func wrapInputError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: xdotool failed: %v", system.ErrDesktopUnavailable, err)
}

type execProcess struct {
	command *exec.Cmd
	stdout  ioReadCloser
}

func (p *execProcess) Stdout() ioReadCloser { return p.stdout }
func (p *execProcess) Wait() error          { return p.command.Wait() }
func (p *execProcess) Kill() error {
	if p.command.Process == nil {
		return nil
	}
	return p.command.Process.Kill()
}

func startProcess(ctx context.Context, name string, arguments ...string) (Process, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = command.Start(); err != nil {
		return nil, err
	}
	return &execProcess{command: command, stdout: output}, nil
}

var _ system.DesktopDriver = (*Driver)(nil)
