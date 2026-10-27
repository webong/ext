package media

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	// DefaultRecordingDuration bounds one caller-started recording.
	DefaultRecordingDuration = time.Minute
	// MaxRecordingDuration is the library ceiling for a recording.
	MaxRecordingDuration = time.Hour
	// DefaultRecordingBytes bounds a retained recording.
	DefaultRecordingBytes int64 = 128 << 20
	// MaxRecordingBytes is the library ceiling for a recording.
	MaxRecordingBytes int64 = 1 << 30
)

// RecordingOptions selects a caller-owned recording. Format names a container;
// the backend rejects a format it cannot produce rather than substituting one.
type RecordingOptions struct {
	Format      string
	FrameRate   int
	MaxDuration time.Duration
	MaxBytes    int64
}

// Normalize supplies defaults and rejects unsupported values.
func (o RecordingOptions) Normalize() (RecordingOptions, error) {
	if o.Format != "webm" && o.Format != "mp4" {
		return o, errors.New("recording format must be webm or mp4")
	}
	if o.FrameRate == 0 {
		o.FrameRate = 30
	}
	if o.FrameRate < 1 || o.FrameRate > 60 {
		return o, errors.New("recording frame rate must be between 1 and 60")
	}
	if o.MaxDuration == 0 {
		o.MaxDuration = DefaultRecordingDuration
	}
	if o.MaxDuration < time.Second || o.MaxDuration > MaxRecordingDuration {
		return o, errors.New("recording duration must be between one second and one hour")
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = DefaultRecordingBytes
	}
	if o.MaxBytes < 1 || o.MaxBytes > MaxRecordingBytes {
		return o, errors.New("recording byte bound is out of range")
	}
	return o, nil
}

// Recording is a bounded, caller-owned stream of encoded output. Reading is
// transport-free; persistence, publishing, and retention belong to the caller.
// Stop ends the native recording and must be safe to call after an error.
type Recording interface {
	io.ReadCloser
	Stop(context.Context) error
}

// Recorder starts a recording from the desktop or surface its implementation
// owns. The caller supplies the bounds, output format, and lifetime.
type Recorder interface {
	Record(context.Context, RecordingOptions) (Recording, error)
}
