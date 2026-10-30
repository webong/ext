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
	"errors"
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
	// Start launches a native recording process. The recorder writes encoded
	// video to descriptor 3 (pipe:3); stdout contains native diagnostics.
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
// muxer names. mp4 cannot be written to the non-seekable video pipe
// (pipe:3) because MP4 requires a seekable output for its moov atom, so only
// webm is supported for piped output. This is an explicit rejection, not a
// silent substitution.
func codecAndMuxer(format string) (codec, muxer string, err error) {
	switch format {
	case "webm":
		return "libvpx-vp9", "webm", nil
	default:
		return "", "", fmt.Errorf("unsupported recording format %q; only piped webm is supported", format)
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
		// The fps filter also works with wf-recorder releases predating --framerate.
		"--filter", "fps=" + strconv.Itoa(options.FrameRate),
		"--codec", codec,
		"--no-damage",
		"--codec-param", "deadline=realtime",
		"--codec-param", "lag-in-frames=0",
		"--muxer", muxer,
		"--file", "pipe:3",
	}
	if d.output != "" {
		arguments = append([]string{"--output", d.output}, arguments...)
	}
	process, err := d.starter(ctx, d.recorder, arguments...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s failed to start: %v", system.ErrDesktopUnavailable, d.recorder, err)
	}
	r := &recording{
		process:         process,
		output:          process.Stdout(),
		maxBytes:        options.MaxBytes,
		maxDuration:     options.MaxDuration,
		started:         time.Now(),
		done:            make(chan struct{}),
		shutdownTimeout: 2 * time.Second,
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

	state           recordingState
	terminalErr     error
	shutdownErr     error
	shutdownTimeout time.Duration

	done         chan struct{}
	shutdownOnce sync.Once
	mu           sync.Mutex
}

// recordingState tracks the lifecycle of a recording.
type recordingState int

const (
	recordingOpen recordingState = iota
	recordingTerminal
)

// watchDuration actively stops the recorder when MaxDuration elapses, even if
// a Read is blocked on the native pipe. It fires once and is idempotent.
func (r *recording) watchDuration(ctx context.Context) {
	timer := time.NewTimer(time.Until(r.started.Add(r.maxDuration)))
	defer timer.Stop()
	select {
	case <-timer.C:
		r.markTerminal(media.ErrRecordingLimit)
	case <-ctx.Done():
		r.markTerminal(ctx.Err())
	case <-r.done:
	}
}

// markTerminal atomically selects and records the first terminal state. The
// first caller wins; all subsequent callers observe the same result. Returns
// the terminal error that applies to this recording (nil for a normal stop,
// which Read reports as io.EOF).
func (r *recording) markTerminal(err error) error {
	r.mu.Lock()
	if r.state == recordingTerminal {
		err = r.terminalErr
		r.mu.Unlock()
		return err
	}
	r.state = recordingTerminal
	r.terminalErr = err
	r.mu.Unlock()
	// Initiate shutdown in the background so the caller is not blocked on
	// native process exit. Stop() waits for completion synchronously.
	go r.shutdown()
	return err
}

// shutdown performs the actual native process shutdown: graceful SIGINT first,
// then a bounded forced kill. Runs exactly once via shutdownOnce.
func (r *recording) shutdown() {
	r.shutdownOnce.Do(func() {
		defer close(r.done)
		// Terminal reads cannot deliver more data. Close the pipe now to
		// release a blocked Read even if the child never acknowledges exit.
		_ = r.output.Close()
		done := r.process.Done()
		if r.process.Signal(os.Interrupt) == nil && waitProcess(done, r.shutdownTimeout) {
			return
		}
		killErr := r.process.Kill()
		if waitProcess(done, r.shutdownTimeout) {
			return // A concurrent exit can make Kill report os.ErrProcessDone.
		}
		r.mu.Lock()
		r.shutdownErr = errors.Join(ErrRecordingShutdown, killErr)
		r.mu.Unlock()
	})
}

// ErrRecordingShutdown means the recorder did not confirm exit within the
// bounded shutdown interval. It may still be running if the native kill failed.
var ErrRecordingShutdown = errors.New("wayland recorder did not exit after shutdown")

func waitProcess(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// Read reads up to len(p) bytes of encoded output, never exceeding the
// remaining MaxBytes allowance. If a duration or byte limit has been reached,
// Read returns the terminal error (ErrRecordingLimit) without delivering data.
// After a normal Stop, Read returns io.EOF.
func (r *recording) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.state == recordingTerminal {
		terminalErr := r.terminalErr
		r.mu.Unlock()
		if terminalErr != nil {
			return 0, terminalErr
		}
		return 0, io.EOF
	}
	if time.Since(r.started) >= r.maxDuration {
		r.state = recordingTerminal
		r.terminalErr = media.ErrRecordingLimit
		r.mu.Unlock()
		go r.shutdown()
		return 0, media.ErrRecordingLimit
	}
	// Never read more than the remaining byte allowance.
	remaining := r.maxBytes - r.bytesRead
	if remaining <= 0 {
		r.state = recordingTerminal
		r.terminalErr = media.ErrRecordingLimit
		r.mu.Unlock()
		go r.shutdown()
		return 0, media.ErrRecordingLimit
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	reader := r.output
	r.mu.Unlock()

	n, readErr := reader.Read(p)

	r.mu.Lock()
	// If a terminal state was recorded while the read was blocked, report it
	// and discard any data that was read.
	if r.state == recordingTerminal {
		terminalErr := r.terminalErr
		r.mu.Unlock()
		if terminalErr != nil {
			return 0, terminalErr
		}
		return 0, io.EOF
	}
	r.bytesRead += int64(n)

	if r.bytesRead >= r.maxBytes {
		r.state = recordingTerminal
		r.terminalErr = media.ErrRecordingLimit
		r.mu.Unlock()
		go r.shutdown()
		if readErr == nil || readErr == io.EOF {
			readErr = media.ErrRecordingLimit
		}
		return n, readErr
	}
	r.mu.Unlock()
	return n, readErr
}

// Close ends the recording. It is safe to call after an error and after Stop;
// repeated calls are idempotent.
func (r *recording) Close() error { return r.Stop(context.Background()) }

// Stop ends the recording gracefully: SIGINT to the recorder, then a bounded
// forced kill if it does not exit. It is safe to call after an error and after
// Close; repeated calls return the same result. If a limit was already
// reached, that limit error is returned rather than the child's exit status
// from the deliberate stop. An unconfirmed exit also returns
// ErrRecordingShutdown. Cancellation while waiting releases that caller; it
// does not replace the terminal state or stop background cleanup.
func (r *recording) Stop(ctx context.Context) error {
	err := r.markTerminal(ctx.Err())
	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(err, r.shutdownErr)
}

// Process exposes nonblocking observation of child exit. Done must return a
// stable, non-nil channel that closes after the child is reaped. The starter
// owns reaping; the recording never launches a waiter for an injected process.
// Signal, Kill and the stdout reader's Close must return promptly; Close must
// unblock Read. A shutdown timeout reports failure, not confirmed process exit.
type Process interface {
	Stdout() io.ReadCloser
	Done() <-chan struct{}
	Kill() error
	Signal(os.Signal) error
}

type Starter func(context.Context, string, ...string) (Process, error)

func startProcess(ctx context.Context, name string, arguments ...string) (Process, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	return startCommand(command)
}

func startCommand(command *exec.Cmd) (Process, error) {
	// Own the read end so the reaper cannot close it before buffered output
	// has been consumed, as Cmd.Wait would do with Cmd.StdoutPipe.
	stdout, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// wf-recorder writes informational messages to stdout even without --log.
	// Keep them out of the encoded stream by giving FFmpeg a dedicated fd.
	command.ExtraFiles = []*os.File{writer}
	command.Stdout = command.Stderr
	if err = command.Start(); err != nil {
		_ = writer.Close()
		_ = stdout.Close()
		return nil, err
	}
	_ = writer.Close()
	p := &execProcess{command: command, stdout: stdout, done: make(chan struct{})}
	go func() {
		_ = command.Wait()
		close(p.done)
	}()
	return p, nil
}

type execProcess struct {
	command *exec.Cmd
	stdout  io.ReadCloser
	done    chan struct{}
}

func (p *execProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *execProcess) Done() <-chan struct{} { return p.done }
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
