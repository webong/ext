// Package ffmpeg implements media.Recorder for the desktop of macOS and Windows with the
// ffmpeg command-line tool. It owns ffmpeg invocation and the bounded recording process; the
// caller selects the capture source, the bounds and the destination, and the adapter chooses
// no display, device, output path or approval flow.
//
// It is an adapter because ffmpeg is an external tool. The portable desktop code that captures
// and sends input on those platforms, with no tool at all, is surface.NativeDesktop in
// res/system; pass this recorder to it as NativeDesktopOptions.Recorder to record.
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webong/ext/res/system/capture/media"
)

// ErrUnsupported means this recorder has no way to capture the desktop of the platform.
var ErrUnsupported = errors.New("ffmpeg desktop recording is unsupported on this platform")

// Process is the running ffmpeg process.
type Process interface {
	Stdout() io.ReadCloser
	Wait() error
	Kill() error
}

// Starter starts a process; the default runs the real tool.
type Starter func(context.Context, string, ...string) (Process, error)

// Config selects what to record and how to run ffmpeg.
type Config struct {
	// Source is the ffmpeg input the caller chose.
	//
	// On macOS it is required and names an AVFoundation device, "video:audio", for example
	// "1:none" for the second video device without audio. List them with
	// ffmpeg -f avfoundation -list_devices true -i "". On Windows it is a gdigrab target and
	// defaults to "desktop", the whole virtual screen.
	Source string
	// Binary names the ffmpeg executable. Empty selects "ffmpeg".
	Binary string
	// OS overrides runtime.GOOS; it exists for tests.
	OS string
	// Start overrides how processes are started; it exists for tests.
	Start Starter
}

// Recorder records the desktop through ffmpeg.
type Recorder struct {
	inputFormat string
	source      string
	binary      string
	start       Starter
}

// New checks the platform and the source and returns a recorder.
func New(config Config) (*Recorder, error) {
	goos := config.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	source := strings.TrimSpace(config.Source)
	var format string
	switch goos {
	case "darwin":
		format = "avfoundation"
		if source == "" {
			return nil, errors.New(`macOS recording needs Config.Source, an AVFoundation device such as "1:none"`)
		}
	case "windows":
		format = "gdigrab"
		if source == "" {
			source = "desktop"
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, goos)
	}
	if len(source) > 256 || strings.HasPrefix(source, "-") || strings.ContainsAny(source, "\x00\r\n") {
		return nil, errors.New("recording source must be one ffmpeg input without a leading dash or control characters")
	}
	if config.Binary == "" {
		config.Binary = "ffmpeg"
	}
	if config.Start == nil {
		config.Start = startProcess
	}
	return &Recorder{inputFormat: format, source: source, binary: config.Binary, start: config.Start}, nil
}

// Record starts a bounded recording. The output is a fragmented MP4 or a streaming WebM written
// to the returned stream, never to a file; ffmpeg stops by itself at the duration and byte
// bounds in options.
func (r *Recorder) Record(ctx context.Context, options media.RecordingOptions) (media.Recording, error) {
	options, err := options.Normalize()
	if err != nil {
		return nil, err
	}
	codec := []string{"-c:v", "libx264", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4"}
	if options.Format == "webm" {
		codec = []string{"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-f", "webm"}
	}
	arguments := []string{
		"-y", "-loglevel", "error", "-f", r.inputFormat,
		"-framerate", strconv.Itoa(options.FrameRate), "-i", r.source,
		"-t", strconv.Itoa(int(options.MaxDuration / time.Second)),
		"-fs", strconv.FormatInt(options.MaxBytes, 10),
	}
	process, err := r.start(ctx, r.binary, append(append(arguments, codec...), "pipe:1")...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed to start: %w", err)
	}
	return &recording{process: process, output: process.Stdout()}, nil
}

type recording struct {
	process Process
	output  io.ReadCloser
	once    sync.Once
}

func (r *recording) Read(buffer []byte) (int, error) { return r.output.Read(buffer) }

func (r *recording) Close() error { return r.Stop(context.Background()) }

// Stop ends the recording. Fragmented output stays playable up to the last complete fragment.
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

var _ media.Recorder = (*Recorder)(nil)
