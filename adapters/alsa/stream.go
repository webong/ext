package alsa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/webong/ext/res/system/audio"
	"github.com/webong/ext/res/system/media"
)

// Format is the caller's PCM16 stream format. ALSA tools receive every value
// explicitly; no device name or card default selects a format.
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
		return f, errors.New("invalid ALSA PCM format")
	}
	return f, nil
}

func (f Format) packetBytes() int {
	return f.SampleRate * f.Channels * 2 * f.PacketMillis / 1_000
}

func (f Format) arguments(pcm string) []string {
	return []string{
		"-q", "-D", pcm, "-f", "S16_LE",
		"-r", fmt.Sprintf("%d", f.SampleRate),
		"-c", fmt.Sprintf("%d", f.Channels),
		"-t", "raw",
	}
}

// Process is the native stream boundary.
type Process interface {
	Stdout() io.ReadCloser
	Stdin() io.WriteCloser
	Wait() error
	Kill() error
}

type Starter func(context.Context, string, ...string) (Process, error)

// Backend implements audio.CaptureBackend and audio.PlaybackBackend with
// arecord and aplay.
type Backend struct {
	start             Starter
	captureExecutable string
	playExecutable    string
	// Format is optional; zero fields select 48 kHz stereo 20 ms packets.
	Format Format
}

func NewBackend(start Starter) *Backend {
	if start == nil {
		start = startProcess
	}
	return &Backend{start: start, captureExecutable: "arecord", playExecutable: "aplay"}
}

func (b *Backend) OpenCapture(ctx context.Context, deviceID string, limits audio.Limits) (audio.Source, error) {
	pcm, err := splitDevice(deviceID, audio.KindInput)
	if err != nil {
		return nil, err
	}
	return b.open(ctx, pcm, limits, false)
}

func (b *Backend) OpenPlayback(ctx context.Context, deviceID string, limits audio.Limits) (audio.Sink, error) {
	pcm, err := splitDevice(deviceID, audio.KindOutput)
	if err != nil {
		return nil, err
	}
	return b.open(ctx, pcm, limits, true)
}

func (b *Backend) open(ctx context.Context, pcm string, limits audio.Limits, playback bool) (*stream, error) {
	limits, err := limits.Normalize()
	if err != nil {
		return nil, err
	}
	format, err := b.Format.normalize()
	if err != nil {
		return nil, err
	}
	executable := b.captureExecutable
	if playback {
		executable = b.playExecutable
	}
	process, err := b.start(ctx, executable, format.arguments(pcm)...)
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
	mu             sync.Mutex
	closed         bool
}

func (s *stream) Read(ctx context.Context) (media.PCM16, error) {
	if s.playback {
		return media.PCM16{}, errors.New("capture stream is not readable")
	}
	if err := ctx.Err(); err != nil {
		return media.PCM16{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return media.PCM16{}, io.EOF
	}
	size := s.format.packetBytes()
	if size > s.maxPacketBytes {
		size = s.maxPacketBytes - s.maxPacketBytes%(2*s.format.Channels)
	}
	if size <= 0 {
		return media.PCM16{}, audio.ErrLimit
	}
	data := make([]byte, size)
	read, err := io.ReadFull(s.reader, data)
	if err != nil && read == 0 {
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
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("playback stream is closed")
	}
	_, err := s.writer.Write(packet.Data)
	return err
}

func (s *stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.writer != nil {
		_ = s.writer.Close()
	}
	if s.reader != nil {
		_ = s.reader.Close()
	}
	_ = s.process.Kill()
	return s.process.Wait()
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
	if err = command.Start(); err != nil {
		return nil, err
	}
	return &execProcess{command: command, stdin: stdin, stdout: stdout}, nil
}

var _ audio.CaptureBackend = (*Backend)(nil)
var _ audio.PlaybackBackend = (*Backend)(nil)
