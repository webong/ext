// Package wayland implements the res/system desktop-driver contract with the
// Wayland command-line tools. It owns grim, wtype, ydotool, and compositor
// interaction; the caller selects this driver and the bounds.
//
// Wayland denies arbitrary window targeting to arbitrary clients, so this
// adapter only supports output-scoped capture (grim -o <output>) and refuses
// window-scoped operations explicitly. Recording requires a compositor
// recorder (wf-recorder) which the caller must explicitly configure.
package wayland

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
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

type Config struct {
	Run     CommandRunner
	Capture string
	Input   string
	// Pointer is optional. When set to "ydotool", pointer operations are
	// forwarded to ydotool, which requires a running ydotoold daemon and
	// /dev/uinput access. Empty keeps pointer operations explicitly unavailable.
	Pointer string
	// Output is optional. When set, grim captures only this output
	// (grim -o <output>). Empty captures the whole desktop.
	Output string
	// Recorder is optional. When set to "wf-recorder", recording is forwarded
	// to wf-recorder. Empty keeps recording explicitly unavailable.
	Recorder string
	// Start launches a native recording process.
	Start Starter
}

type Driver struct {
	run      CommandRunner
	capture  string
	input    string
	pointer  string
	output   string
	recorder string
	starter  Starter
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
	if config.Start == nil {
		config.Start = startProcess
	}
	return &Driver{run: config.Run, capture: config.Capture, input: config.Input, pointer: config.Pointer, output: config.Output, recorder: config.Recorder, starter: config.Start}
}

func (d *Driver) Name() string { return "wayland" }

func (d *Driver) Capture(ctx context.Context, options system.CaptureOptions) (system.SurfaceCapture, error) {
	arguments := []string{"-"}
	if d.output != "" {
		arguments = append([]string{"-o", d.output}, arguments...)
	}
	data, err := d.run(ctx, d.capture, arguments...)
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
		_, err := d.run(ctx, d.input, "--", input.Text)
		return wrapInputError(err)
	case system.SurfaceInputKey:
		if strings.HasPrefix(input.Key, "-") || strings.Contains(input.Key, " ") {
			return fmt.Errorf("%w: wtype key must be a key name", system.ErrDesktopUnavailable)
		}
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
	case system.SurfaceInputMove, system.SurfaceInputClick, system.SurfaceInputDrag:
		return d.pointerInput(ctx, input)
	case system.SurfaceInputScroll:
		return fmt.Errorf("%w: ydotool has no wheel operation; Wayland scroll is unavailable", system.ErrDesktopUnavailable)
	}
	return fmt.Errorf("%w: unsupported Wayland input kind %q", system.ErrDesktopUnavailable, input.Kind)
}

func (d *Driver) pointerInput(ctx context.Context, input system.SurfaceInput) error {
	if d.pointer == "" {
		return fmt.Errorf("%w: Wayland pointer input requires an explicitly configured ydotool driver", system.ErrDesktopUnavailable)
	}
	button := waylandButton(input.Button)
	if _, err := d.run(ctx, d.pointer, "mousemove", "--absolute", strconv.Itoa(input.Point.X), strconv.Itoa(input.Point.Y)); err != nil {
		return wrapPointerError(err)
	}
	switch input.Kind {
	case system.SurfaceInputMove:
		return nil
	case system.SurfaceInputClick:
		_, err := d.run(ctx, d.pointer, "click", fmt.Sprintf("0x%02X", 0xC0|button))
		return wrapPointerError(err)
	case system.SurfaceInputDrag:
		down := fmt.Sprintf("0x%02X", 0x40|button)
		up := fmt.Sprintf("0x%02X", 0x80|button)
		if _, err := d.run(ctx, d.pointer, "click", down); err != nil {
			return wrapPointerError(err)
		}
		released := false
		defer func() {
			if !released {
				_, _ = d.run(ctx, d.pointer, "click", up)
			}
		}()
		if _, err := d.run(ctx, d.pointer, "mousemove", "--absolute", strconv.Itoa(input.End.X), strconv.Itoa(input.End.Y)); err != nil {
			return wrapPointerError(err)
		}
		_, err := d.run(ctx, d.pointer, "click", up)
		if err == nil {
			released = true
		}
		return wrapPointerError(err)
	}
	return fmt.Errorf("%w: unsupported Wayland pointer kind %q", system.ErrDesktopUnavailable, input.Kind)
}

func waylandButton(button string) int {
	switch button {
	case "right":
		return 0x01
	case "middle":
		return 0x02
	default:
		return 0x00
	}
}

func wrapPointerError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: ydotool failed: %v", system.ErrDesktopUnavailable, err)
}

// codecAndMuxer maps a recording container format to the wf-recorder codec and
// muxer names. mp4 cannot be written to the non-seekable stdout pipe
// (pipe:1) because MP4 requires a seekable output for its moov atom, so only
// webm is supported for piped output. This is an explicit rejection, not a
// silent substitution.
func codecAndMuxer(format string) (codec, muxer string, err error) {
	switch format {
	case "webm":
		return "libvpx-vp9", "webm", nil
	default:
		return "", "", fmt.Errorf("unsupported recording format %q; only webm to stdout is supported", format)
	}
}

// Record starts a bounded recording through the configured recorder. Wayland
// recording requires a compositor recorder (wf-recorder); this adapter does not
// select one and reports ErrDesktopUnavailable when none is configured.
// Window-scoped recording is not supported; output-scoped capture is the only
// desktop-scoped mechanism. No desktop portal or approval flow is used.
func (d *Driver) Record(ctx context.Context, options media.RecordingOptions) (media.Recording, error) {
	if d.recorder == "" {
		return nil, fmt.Errorf("%w: Wayland recording requires a compositor recorder or portal, which this adapter does not select",
			system.ErrDesktopUnavailable)
	}
	options, err := options.Normalize()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", system.ErrInvalidRecording, err)
	}
	codec, muxer, err := codecAndMuxer(options.Format)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", system.ErrInvalidRecording, err)
	}
	arguments := []string{
		"--framerate", strconv.Itoa(options.FrameRate),
		"--codec", codec,
		"--muxer", muxer,
		"--file", "pipe:1",
	}
	if d.output != "" {
		arguments = append([]string{"--output", d.output}, arguments...)
	}
	process, err := d.starter(ctx, d.recorder, arguments...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s failed to start: %v", system.ErrDesktopUnavailable, d.recorder, err)
	}
	r := &recording{
		process:     process,
		output:      process.Stdout(),
		maxBytes:    options.MaxBytes,
		maxDuration: options.MaxDuration,
		started:     time.Now(),
		done:        make(chan struct{}),
	}
	// A blocked read can exceed MaxDuration, so start a watchdog that actively
	// stops the recorder when the duration bound is crossed. The watchdog
	// fires once and is idempotent.
	go r.watchDuration(ctx)
	return r, nil
}

type recording struct {
	process     Process
	output      io.ReadCloser
	maxBytes    int64
	maxDuration time.Duration
	started     time.Time
	bytesRead   int64
	stopped     bool
	limitErr    error
	done        chan struct{}
	mu          sync.Mutex
	once        sync.Once
}

// watchDuration actively stops the recorder when MaxDuration elapses, even if
// a Read is blocked on the native pipe. It fires once and is idempotent.
func (r *recording) watchDuration(ctx context.Context) {
	timer := time.NewTimer(time.Until(r.started.Add(r.maxDuration)))
	defer timer.Stop()
	select {
	case <-timer.C:
		r.stop(media.ErrRecordingLimit)
	case <-ctx.Done():
		r.stop(ctx.Err())
	case <-r.done:
	}
}

// stop is the single idempotent shutdown path used by both automatic limit
// enforcement and Stop/Close. It signals the recorder gracefully first, then
// uses a bounded forced kill if it does not exit.
func (r *recording) stop(limitErr error) {
	r.once.Do(func() {
		close(r.done)
		// Graceful: ask the recorder to finish writing its trailer.
		if sigErr := r.process.Signal(os.Interrupt); sigErr == nil {
			done := make(chan error, 1)
			go func() { done <- r.process.Wait() }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = r.process.Kill()
				<-done
			}
		} else {
			_ = r.process.Kill()
			_ = r.process.Wait()
		}
		_ = r.output.Close()
	})
	// Capture the terminal result from the first caller; subsequent calls
	// return their own context error if any, but never overwrite a recorded
	// terminal result.
	r.mu.Lock()
	if r.limitErr == nil && limitErr != nil {
		r.limitErr = limitErr
	}
	r.stopped = true
	r.mu.Unlock()
}

func (r *recording) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		// After a normal stop, report EOF. After a limit, report the limit
		// error. After cancellation, report the context error.
		if r.limitErr != nil {
			return 0, r.limitErr
		}
		return 0, io.EOF
	}
	if time.Since(r.started) >= r.maxDuration {
		r.mu.Unlock()
		r.stop(media.ErrRecordingLimit)
		return 0, media.ErrRecordingLimit
	}
	// Never read more than the remaining byte allowance.
	remaining := r.maxBytes - r.bytesRead
	if remaining <= 0 {
		r.mu.Unlock()
		r.stop(media.ErrRecordingLimit)
		return 0, media.ErrRecordingLimit
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	reader := r.output
	r.mu.Unlock()

	n, err := reader.Read(p)

	r.mu.Lock()
	r.bytesRead += int64(n)
	// If the watchdog or another Read already stopped this recording, report
	// the terminal result even if the underlying reader returned data.
	if r.stopped {
		if r.limitErr != nil {
			r.mu.Unlock()
			return 0, r.limitErr
		}
		r.mu.Unlock()
		return 0, io.EOF
	}
	if r.bytesRead >= r.maxBytes {
		limitErr := media.ErrRecordingLimit
		r.stopped = true
		r.limitErr = limitErr
		r.mu.Unlock()
		// Actively stop the recorder now that the byte bound is reached.
		r.stop(limitErr)
		if err == nil || err == io.EOF {
			err = limitErr
		}
		return n, err
	}
	r.mu.Unlock()
	return n, err
}

func (r *recording) Close() error { return r.Stop(context.Background()) }

// Stop ends the recording gracefully: SIGINT to the recorder, then a bounded
// forced kill if it does not exit. It is safe to call after an error and after
// Close; repeated calls are idempotent. If a limit was already reached, that
// limit error is returned rather than the child's exit status from the
// deliberate stop.
func (r *recording) Stop(ctx context.Context) error {
	r.mu.Lock()
	limitErr := r.limitErr
	alreadyStopped := r.stopped
	r.mu.Unlock()
	if alreadyStopped {
		return limitErr
	}
	// Only the first Stop/Close performs the actual shutdown; concurrent or
	// later calls wait for it and return the recorded result.
	r.stop(ctx.Err())
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.limitErr != nil {
		return r.limitErr
	}
	return nil
}

type Process interface {
	Stdout() io.ReadCloser
	Wait() error
	Kill() error
	Signal(os.Signal) error
}

type Starter func(context.Context, string, ...string) (Process, error)

func startProcess(ctx context.Context, name string, arguments ...string) (Process, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = command.Start(); err != nil {
		return nil, err
	}
	return &execProcess{command: command, stdout: stdout}, nil
}

type execProcess struct {
	command *exec.Cmd
	stdout  io.ReadCloser
}

func (p *execProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *execProcess) Wait() error           { return p.command.Wait() }
func (p *execProcess) Kill() error {
	if p.command.Process == nil {
		return nil
	}
	return p.command.Process.Kill()
}
func (p *execProcess) Signal(sig os.Signal) error {
	if p.command.Process == nil {
		return nil
	}
	return p.command.Process.Signal(sig)
}

var _ system.DesktopDriver = (*Driver)(nil)

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
	if key == "" {
		return key
	}
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
