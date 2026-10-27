// Package audio defines bounded, transport-free audio device observation and
// caller-selected capture or playback. It opens no device, process, or
// connection itself: a backend such as the PulseAudio adapter implements the
// interfaces, and the consumer selects the device and grants access.
package audio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webong/ext/res/system/media"
)

const (
	// DefaultMaxDevices bounds one observation.
	DefaultMaxDevices = 64
	// MaxDevices is the library ceiling for MaxDevices.
	MaxDevices = 1024
	// DefaultMaxPacketBytes bounds one read or written PCM packet.
	DefaultMaxPacketBytes = 192_000
	// MaxPacketBytes is the library ceiling for MaxPacketBytes.
	MaxPacketBytes = 1 << 20
)

// Kind classifies a device without exposing a native endpoint or routing rule.
type Kind string

const (
	KindOutput  Kind = "output"
	KindInput   Kind = "input"
	KindMonitor Kind = "monitor"
)

// Device is one observed endpoint. ID is backend-local and opaque to the
// library; Name is display metadata. Sample values are zero when the backend
// does not know them.
type Device struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         Kind   `json:"kind"`
	State        string `json:"state,omitempty"`
	SampleFormat string `json:"sample_format,omitempty"`
	SampleRate   int    `json:"sample_rate,omitempty"`
	Channels     int    `json:"channels,omitempty"`
}

// Status distinguishes an empty device list from an unavailable backend.
type Status struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Snapshot is one bounded observation. Backend names the selected native
// mechanism for diagnostics; the library never chooses it.
type Snapshot struct {
	Backend    string    `json:"backend"`
	ObservedAt time.Time `json:"observed_at"`
	Devices    []Device  `json:"devices"`
	Status     Status    `json:"status"`
}

// Limits bound an observation and one PCM packet. Zero values select defaults.
type Limits struct {
	MaxDevices     int `json:"max_devices"`
	MaxPacketBytes int `json:"max_packet_bytes"`
}

func (l Limits) Normalize() (Limits, error) {
	if l.MaxDevices == 0 {
		l.MaxDevices = DefaultMaxDevices
	}
	if l.MaxPacketBytes == 0 {
		l.MaxPacketBytes = DefaultMaxPacketBytes
	}
	if l.MaxDevices < 1 || l.MaxDevices > MaxDevices {
		return l, fmt.Errorf("%w: device bound must be between 1 and %d", ErrInvalid, MaxDevices)
	}
	if l.MaxPacketBytes < 1 || l.MaxPacketBytes > MaxPacketBytes {
		return l, fmt.Errorf("%w: packet bound must be between 1 and %d", ErrInvalid, MaxPacketBytes)
	}
	return l, nil
}

var (
	// ErrInvalid reports a malformed device, snapshot, or packet.
	ErrInvalid = errors.New("invalid audio value")
	// ErrLimit reports a value beyond a caller-selected bound.
	ErrLimit = errors.New("audio limit exceeded")
	// ErrUnavailable reports a backend that cannot observe or open the device.
	ErrUnavailable = errors.New("audio backend is unavailable")
)

// Observer reports caller-visible local audio devices. It sends no audio and
// starts no stream.
type Observer interface {
	Observe(context.Context, Limits) (Snapshot, error)
}

type ObserverFunc func(context.Context, Limits) (Snapshot, error)

func (f ObserverFunc) Observe(ctx context.Context, limits Limits) (Snapshot, error) {
	return f(ctx, limits)
}

// CaptureBackend opens a bounded source for one caller-selected device.
type CaptureBackend interface {
	OpenCapture(context.Context, string, Limits) (Source, error)
}

// PlaybackBackend opens a bounded sink for one caller-selected device.
type PlaybackBackend interface {
	OpenPlayback(context.Context, string, Limits) (Sink, error)
}

// Source produces bounded PCM16 packets. Read must respect the limits supplied
// when the source was opened and must not block forever after context
// cancellation.
type Source interface {
	Read(context.Context) (media.PCM16, error)
	Close() error
}

// Sink consumes bounded PCM16 packets and reports backpressure through Write.
type Sink interface {
	Write(context.Context, media.PCM16) error
	Close() error
}

// ValidateSnapshot checks identity, kind, format, and device bounds.
func ValidateSnapshot(snapshot Snapshot, limits Limits) error {
	limits, err := limits.Normalize()
	if err != nil {
		return err
	}
	if snapshot.Backend == "" || len(snapshot.Backend) > 64 || snapshot.ObservedAt.IsZero() {
		return fmt.Errorf("%w: snapshot requires a backend and observation time", ErrInvalid)
	}
	if snapshot.Status.State != "complete" && snapshot.Status.State != "partial" && snapshot.Status.State != "unsupported" {
		return fmt.Errorf("%w: snapshot status must be complete, partial, or unsupported", ErrInvalid)
	}
	if len(snapshot.Devices) > limits.MaxDevices {
		return fmt.Errorf("%w: device bound exceeded", ErrLimit)
	}
	seen := map[string]bool{}
	for _, device := range snapshot.Devices {
		if device.ID == "" || len(device.ID) > 256 || !printable(device.ID) {
			return fmt.Errorf("%w: device ID is required and bounded", ErrInvalid)
		}
		if seen[device.ID] {
			return fmt.Errorf("%w: duplicate device ID %q", ErrInvalid, device.ID)
		}
		seen[device.ID] = true
		if device.Name == "" || len(device.Name) > 256 || !printable(device.Name) {
			return fmt.Errorf("%w: device name is required and bounded", ErrInvalid)
		}
		switch device.Kind {
		case KindOutput, KindInput, KindMonitor:
		default:
			return fmt.Errorf("%w: unknown device kind %q", ErrInvalid, device.Kind)
		}
		if len(device.State) > 64 || len(device.SampleFormat) > 64 {
			return fmt.Errorf("%w: device metadata is too long", ErrInvalid)
		}
		if device.SampleRate != 0 && (device.SampleRate < 8_000 || device.SampleRate > 192_000) {
			return fmt.Errorf("%w: device sample rate is out of range", ErrInvalid)
		}
		if device.Channels != 0 && (device.Channels < 1 || device.Channels > 8) {
			return fmt.Errorf("%w: device channel count is out of range", ErrInvalid)
		}
	}
	return nil
}

// ValidatePacket checks PCM format, alignment, and the caller's packet ceiling.
func ValidatePacket(packet media.PCM16, limits Limits) error {
	limits, err := limits.Normalize()
	if err != nil {
		return err
	}
	if err = packet.Validate(limits.MaxPacketBytes); err != nil {
		return fmt.Errorf("%w: %v", ErrLimit, err)
	}
	return nil
}

// Select returns the first device of a kind, or the explicitly named one. It
// never falls back to a different kind or to a default device.
func Select(snapshot Snapshot, kind Kind, id string) (Device, error) {
	if id != "" {
		for _, device := range snapshot.Devices {
			if device.ID == id && device.Kind == kind {
				return device, nil
			}
		}
		return Device{}, fmt.Errorf("%w: device %q is not an observed %s", ErrUnavailable, id, kind)
	}
	for _, device := range snapshot.Devices {
		if device.Kind == kind {
			return device, nil
		}
	}
	return Device{}, fmt.Errorf("%w: no observed %s device", ErrUnavailable, kind)
}

func printable(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return strings.TrimSpace(value) == value
}
