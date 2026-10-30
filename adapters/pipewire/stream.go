// Package pipewire implements bounded capture and playback streams for PipeWire
// through pw-record and pw-play.
package pipewire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/webong/ext/res/system/audio"
	"github.com/webong/ext/res/system/media"
)

// stopTimeout bounds a normal stream shutdown before the process is killed.
const stopTimeout = 500 * time.Millisecond

// Format is the caller's PCM16 stream format. PipeWire tools accept these
// values explicitly; no format is inferred from a device name.
type Format struct {
	SampleRate   int
	Channels     int
	PacketMillis int
}

func (f Format) normalize() (Format, error) {
	if f.SampleRate == 0 {
		f.SampleRate = 48_000
	}
	if f.Channels == 0 {
		f.Channels = 2
	}
	if f.PacketMillis == 0 {
		f.PacketMillis = 20
	}
	if f.SampleRate < 8_000 || f.SampleRate > 192_000 || f.Channels < 1 || f.Channels > 8 ||
		f.PacketMillis < 1 || f.PacketMillis > 1_000 {
		return f, errors.New("invalid PipeWire PCM format")
	}
	return f, nil
}

func (f Format) packetBytes() int {
	return f.SampleRate * f.Channels * 2 * f.PacketMillis / 1_000
}

// arguments is the shared pw-record/pw-play interface. Both tools select the
// target with --target, take the PipeWire sample format name (s16), and read or
// write the stream on the explicit '-' path. pw-play needs no mode flag.
func (f Format) arguments(deviceID string) []string {
	return []string{
		"--target=" + deviceID, "--format=s16",
		fmt.Sprintf("--rate=%d", f.SampleRate), fmt.Sprintf("--channels=%d", f.Channels),
		"-", // Explicit format selects raw pipes, including older pw-cat versions without --raw.
	}
}

// Process is the native stream boundary.
type Process interface {
	Stdout() io.ReadCloser
	Stdin() io.WriteCloser
	Wait() error
	Kill() error
}

// Starter launches a native audio process.
type Starter func(context.Context, string, ...string) (Process, error)

// Backend implements audio.CaptureBackend and audio.PlaybackBackend with
// pw-record and pw-play.
type Backend struct {
	start              Starter
	captureExecutable  string
	playbackExecutable string
	// Format is optional; zero fields select 48 kHz stereo 20 ms packets.
	Format Format
}

func NewBackend(start Starter) *Backend {
	if start == nil {
		start = startProcess
	}
	return &Backend{start: start, captureExecutable: "pw-record", playbackExecutable: "pw-play"}
}

func (b *Backend) OpenCapture(ctx context.Context, deviceID string, limits audio.Limits) (audio.Source, error) {
	return b.open(ctx, deviceID, limits, false)
}

func (b *Backend) OpenPlayback(ctx context.Context, deviceID string, limits audio.Limits) (audio.Sink, error) {
	return b.open(ctx, deviceID, limits, true)
}

func (b *Backend) open(ctx context.Context, deviceID string, limits audio.Limits, playback bool) (*stream, error) {
	limits, err := limits.Normalize()
	if err != nil {
		return nil, err
	}
	if err = validateDevice(deviceID); err != nil {
		return nil, err
	}
	format, err := b.Format.normalize()
	if err != nil {
		return nil, err
	}
	executable := b.captureExecutable
	if executable == "" {
		executable = "pw-record"
	}
	if playback {
		executable = b.playbackExecutable
		if executable == "" {
			executable = "pw-play"
		}
	}
	process, err := b.start(ctx, executable, format.arguments(deviceID)...)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot start %s: %v", audio.ErrUnavailable, executable, err)
	}
	result := &stream{process: process, format: format, playback: playback, maxPacketBytes: limits.MaxPacketBytes}
	if playback {
		result.writer = process.Stdin()
	} else {
		result.reader = process.Stdout()
	}
	return result, nil
}

type stream struct {
	process        Process
	reader         io.ReadCloser
	writer         io.WriteCloser
	format         Format
	maxPacketBytes int
	playback       bool

	mu          sync.Mutex
	closed      bool
	interrupted bool
	forced      bool
	closeOnce   sync.Once
	closeErr    error
}

func (s *stream) Read(ctx context.Context) (media.PCM16, error) {
	if s.playback {
		return media.PCM16{}, errors.New("capture stream is not readable")
	}
	if err := ctx.Err(); err != nil {
		return media.PCM16{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return media.PCM16{}, io.EOF
	}
	reader := s.reader
	s.mu.Unlock()
	size := s.format.packetBytes()
	if size > s.maxPacketBytes {
		size = s.maxPacketBytes - s.maxPacketBytes%(2*s.format.Channels)
	}
	if size <= 0 {
		return media.PCM16{}, audio.ErrLimit
	}
	stop := s.watch(ctx)
	defer stop()
	data := make([]byte, size)
	read, err := io.ReadFull(reader, data)
	if err != nil && read == 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return media.PCM16{}, ctxErr
		}
		s.mu.Lock()
		interrupted := s.interrupted
		s.mu.Unlock()
		if interrupted {
			return media.PCM16{}, io.EOF
		}
		return media.PCM16{}, err
	}
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return media.PCM16{}, err
	}
	return media.PCM16{SampleRate: s.format.SampleRate, Channels: s.format.Channels, Data: data[:read]}, nil
}

func (s *stream) Write(ctx context.Context, packet media.PCM16) error {
	if !s.playback {
		return errors.New("playback stream is not writable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := audio.ValidatePacket(packet, audio.Limits{MaxPacketBytes: s.maxPacketBytes}); err != nil {
		return err
	}
	if packet.SampleRate != s.format.SampleRate || packet.Channels != s.format.Channels {
		return fmt.Errorf("%w: packet format does not match the stream", audio.ErrInvalid)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("playback stream is closed")
	}
	writer := s.writer
	s.mu.Unlock()
	stop := s.watch(ctx)
	defer stop()
	_, err := writer.Write(packet.Data)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}

// watch interrupts blocked native I/O when the caller's context ends. A
// cancelled stream is permanently closed.
func (s *stream) watch(ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.interrupt()
		case <-done:
		}
	}()
	return func() { close(done) }
}

func (s *stream) interrupt() {
	s.mu.Lock()
	if s.interrupted || s.closed {
		s.mu.Unlock()
		return
	}
	s.interrupted = true
	s.forced = true
	reader, writer, process := s.reader, s.writer, s.process
	s.mu.Unlock()
	if writer != nil {
		_ = writer.Close()
	}
	if reader != nil {
		_ = reader.Close()
	}
	_ = process.Kill()
}

func (s *stream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		reader, writer, process := s.reader, s.writer, s.process
		s.mu.Unlock()
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		// A child that had already exited on its own reports its status. Once
		// Close stops the stream, the exit status is a consequence of that
		// deliberate stop and is not surfaced as a failure.
		select {
		case err := <-done:
			s.recordExit(err)
			return
		default:
		}
		if writer != nil {
			_ = writer.Close()
		}
		if reader != nil {
			_ = reader.Close()
		}
		timer := time.NewTimer(stopTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			s.mu.Lock()
			s.forced = true
			s.mu.Unlock()
			_ = process.Kill()
			<-done
		}
	})
	return s.closeErr
}

func (s *stream) recordExit(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.interrupted && !s.forced {
		s.closeErr = err
	}
}

type execProcess struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
}

func (p *execProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Wait() error           { return p.command.Wait() }
func (p *execProcess) Kill() error {
	if p.command.Process == nil {
		return nil
	}
	return p.command.Process.Kill()
}

func startProcess(ctx context.Context, name string, arguments ...string) (Process, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		return nil, err
	}
	return &execProcess{command: command, stdin: stdin, stdout: stdout}, nil
}

var _ audio.CaptureBackend = (*Backend)(nil)
var _ audio.PlaybackBackend = (*Backend)(nil)
