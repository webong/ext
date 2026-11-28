package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"math"
	"time"
)

// Frame is one caller-owned encoded image with its local target identity and
// coordinate system. Capture implementations return bytes directly; network
// envelopes, storage IDs, and retention policy belong to consumers.
type Frame struct {
	Data            []byte
	ObservedAt      time.Time
	Target          string
	CoordinateSpace string
}

// FrameSource captures from a target already selected by its caller.
type FrameSource interface {
	Capture(context.Context) (Frame, error)
}

type FrameSourceFunc func(context.Context) (Frame, error)

func (f FrameSourceFunc) Capture(ctx context.Context) (Frame, error) { return f(ctx) }

type Region struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type FrameMetadata struct {
	Sequence        int       `json:"sequence"`
	ObservedAt      time.Time `json:"observed_at"`
	Target          string    `json:"target"`
	CoordinateSpace string    `json:"coordinate_space"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	Bytes           int       `json:"bytes"`
	SHA256          string    `json:"sha256"`
	ChangedFraction float64   `json:"changed_fraction"`
	ChangedBounds   *Region   `json:"changed_bounds,omitempty"`
}

type SampleManifest struct {
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   time.Time       `json:"finished_at"`
	Sampled      int             `json:"sampled"`
	Retained     int             `json:"retained"`
	SampledBytes int64           `json:"sampled_bytes"`
	Frames       []FrameMetadata `json:"frames"`
	Complete     bool            `json:"complete"`
	StopReason   string          `json:"stop_reason"`
}

type SampleOptions struct {
	MaxFrames     int
	MaxDuration   time.Duration
	Interval      time.Duration
	Heartbeat     time.Duration
	MaxFrameBytes int
	MaxTotalBytes int64
	MaxPixels     int
	MinChange     float64
}

func (o SampleOptions) Validate() error {
	if o.MaxFrames < 1 || o.MaxFrames > 10_000 || o.MaxDuration <= 0 || o.MaxDuration > time.Hour ||
		o.Interval < 20*time.Millisecond || o.Interval > o.MaxDuration || o.Heartbeat < 0 ||
		o.Heartbeat > o.MaxDuration || o.MaxFrameBytes < 1 || o.MaxFrameBytes > maximumMediaBytes ||
		o.MaxTotalBytes < int64(o.MaxFrameBytes) || o.MaxTotalBytes > 1<<30 ||
		o.MaxPixels < 1 || o.MaxPixels > 16<<20 || math.IsNaN(o.MinChange) ||
		math.IsInf(o.MinChange, 0) || o.MinChange < 0 || o.MinChange > 1 {
		return errors.New("invalid capture sampling bounds")
	}
	return nil
}

// FrameConsumer processes one retained frame. Persistence, references, and
// transport remain entirely outside this package.
type FrameConsumer func(context.Context, Frame, FrameMetadata) error

// Sample performs captures under explicit time, frame, pixel, and byte limits.
// Manifest metrics include samples discarded by change filtering.
func Sample(ctx context.Context, source FrameSource, options SampleOptions, consume FrameConsumer) (manifest SampleManifest, err error) {
	manifest.StartedAt = time.Now().UTC()
	defer func() { manifest.FinishedAt = time.Now().UTC() }()
	if source == nil || consume == nil {
		return manifest, errors.New("capture source and consumer are required")
	}
	if err = options.Validate(); err != nil {
		return manifest, err
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, options.MaxDuration)
	defer cancel()
	var previous image.Image
	var target, coordinateSpace string
	var retainedAt time.Time
	for sequence := 0; sequence < options.MaxFrames; sequence++ {
		if err = ctx.Err(); err != nil {
			manifest.StopReason = "duration"
			if parent.Err() == nil {
				manifest.Complete = true
				return manifest, nil
			}
			manifest.StopReason = "canceled"
			return manifest, parent.Err()
		}
		frame, captureErr := source.Capture(ctx)
		if captureErr != nil {
			manifest.StopReason = "capture_failed"
			return manifest, captureErr
		}
		img, decodeErr := decodeFrame(frame, options.MaxFrameBytes, options.MaxPixels)
		if decodeErr != nil {
			manifest.StopReason = "invalid_frame"
			return manifest, decodeErr
		}
		if target == "" {
			target, coordinateSpace = frame.Target, frame.CoordinateSpace
		}
		if frame.Target != target || frame.CoordinateSpace != coordinateSpace {
			manifest.StopReason = "target_changed"
			return manifest, errors.New("capture target changed")
		}
		if manifest.SampledBytes+int64(len(frame.Data)) > options.MaxTotalBytes {
			manifest.StopReason = "byte_limit"
			manifest.Complete = true
			return manifest, nil
		}
		manifest.Sampled++
		manifest.SampledBytes += int64(len(frame.Data))
		fraction, region := Difference(previous, img)
		if previous == nil || fraction > 0 && fraction >= options.MinChange ||
			options.Heartbeat > 0 && time.Since(retainedAt) >= options.Heartbeat {
			digest := sha256.Sum256(frame.Data)
			bounds := img.Bounds()
			metadata := FrameMetadata{
				Sequence: sequence, ObservedAt: frame.ObservedAt, Target: frame.Target,
				CoordinateSpace: frame.CoordinateSpace, Width: bounds.Dx(), Height: bounds.Dy(),
				Bytes: len(frame.Data), SHA256: hex.EncodeToString(digest[:]),
				ChangedFraction: fraction, ChangedBounds: region,
			}
			consumeErr := consume(ctx, frame, metadata)
			if consumeErr != nil {
				manifest.StopReason = "consumer_failed"
				return manifest, consumeErr
			}
			manifest.Frames = append(manifest.Frames, metadata)
			manifest.Retained++
			retainedAt = time.Now()
			previous = img
		}
		if sequence+1 == options.MaxFrames {
			break
		}
		timer := time.NewTimer(options.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	manifest.Complete = true
	manifest.StopReason = "frame_limit"
	return manifest, nil
}

// Around captures before and after one caller-supplied local operation. It
// still returns post-operation evidence after an operation error when the
// context permits it; callers decide how to interpret dispatch uncertainty.
func Around(ctx context.Context, source FrameSource, operation func(context.Context) error) (before, after Frame, err error) {
	if source == nil || operation == nil {
		return before, after, errors.New("capture source and explicit operation are required")
	}
	before, err = source.Capture(ctx)
	if err != nil {
		return before, after, err
	}
	operationErr := operation(ctx)
	after, captureErr := source.Capture(ctx)
	if captureErr == nil && after.Target != before.Target {
		captureErr = errors.New("capture target changed around operation")
	}
	return before, after, errors.Join(operationErr, captureErr)
}

func decodeFrame(frame Frame, maxBytes, maxPixels int) (image.Image, error) {
	if maxBytes < 1 || maxBytes > maximumMediaBytes || maxPixels < 1 || maxPixels > 16<<20 ||
		len(frame.Data) == 0 || len(frame.Data) > maxBytes || frame.ObservedAt.IsZero() ||
		frame.Target == "" || len(frame.Target) > 4_096 || frame.CoordinateSpace == "" ||
		len(frame.CoordinateSpace) > 256 {
		return nil, errors.New("invalid capture frame")
	}
	config, err := png.DecodeConfig(bytes.NewReader(frame.Data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxPixels/config.Height {
		return nil, errors.New("capture image exceeds dimensions or is not PNG")
	}
	return png.Decode(bytes.NewReader(frame.Data))
}

// Difference measures exact pixel changes between equal-sized images. A
// resize reports a completely changed image; bounds use image-local pixels.
func Difference(previous, current image.Image) (float64, *Region) {
	bounds := current.Bounds()
	if previous == nil || previous.Bounds().Size() != bounds.Size() {
		return 1, &Region{Width: bounds.Dx(), Height: bounds.Dy()}
	}
	oldBounds := previous.Bounds()
	count := 0
	minX, minY, maxX, maxY := bounds.Dx(), bounds.Dy(), -1, -1
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			r, g, b, a := current.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			oldR, oldG, oldB, oldA := previous.At(oldBounds.Min.X+x, oldBounds.Min.Y+y).RGBA()
			if r != oldR || g != oldG || b != oldB || a != oldA {
				count++
				minX, minY = min(minX, x), min(minY, y)
				maxX, maxY = max(maxX, x), max(maxY, y)
			}
		}
	}
	if count == 0 {
		return 0, nil
	}
	return float64(count) / float64(bounds.Dx()*bounds.Dy()), &Region{
		X: minX, Y: minY, Width: maxX - minX + 1, Height: maxY - minY + 1,
	}
}
