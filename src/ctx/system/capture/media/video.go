package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// VideoDecoder borrows a caller-selected decoder and media resource. It does
// not open a path, fetch a URL, or launch a codec. FrameAt must apply its own
// decoding limits before returning a PNG frame.
type VideoDecoder interface {
	FrameAt(context.Context, time.Duration) (Frame, error)
}

type VideoOptions struct {
	Offsets       []time.Duration
	MaxDuration   time.Duration
	MaxFrameBytes int
	MaxTotalBytes int64
	MaxPixels     int
}

type VideoFrame struct {
	OffsetMS int64         `json:"offset_ms"`
	Metadata FrameMetadata `json:"metadata"`
}

type VideoManifest struct {
	Frames   []VideoFrame `json:"frames"`
	Bytes    int64        `json:"bytes"`
	Complete bool         `json:"complete"`
}

// SampleVideo extracts caller-selected increasing timestamps exactly once.
func SampleVideo(ctx context.Context, decoder VideoDecoder, options VideoOptions, consume FrameConsumer) (VideoManifest, error) {
	var result VideoManifest
	if decoder == nil || consume == nil || len(options.Offsets) < 1 || len(options.Offsets) > 10_000 ||
		options.MaxDuration <= 0 || options.MaxDuration > time.Hour || options.MaxFrameBytes < 1 ||
		options.MaxFrameBytes > maximumMediaBytes || options.MaxTotalBytes < int64(options.MaxFrameBytes) ||
		options.MaxTotalBytes > 1<<30 || options.MaxPixels < 1 || options.MaxPixels > 16<<20 {
		return result, errors.New("invalid video sampling bounds")
	}
	for index, offset := range options.Offsets {
		if offset < 0 || offset > options.MaxDuration || index > 0 && offset <= options.Offsets[index-1] {
			return result, errors.New("video offsets must increase within the selected duration")
		}
	}
	var target, coordinateSpace string
	for index, offset := range options.Offsets {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		frame, err := decoder.FrameAt(ctx, offset)
		if err != nil {
			return result, err
		}
		img, err := decodeFrame(frame, options.MaxFrameBytes, options.MaxPixels)
		if err != nil {
			return result, err
		}
		if index == 0 {
			target, coordinateSpace = frame.Target, frame.CoordinateSpace
		}
		if frame.Target != target || frame.CoordinateSpace != coordinateSpace {
			return result, errors.New("video target or coordinate space changed")
		}
		if result.Bytes+int64(len(frame.Data)) > options.MaxTotalBytes {
			return result, errors.New("video sampling exceeds total byte ceiling")
		}
		result.Bytes += int64(len(frame.Data))
		digest := sha256.Sum256(frame.Data)
		metadata := FrameMetadata{
			Sequence: index, ObservedAt: frame.ObservedAt, Target: frame.Target,
			CoordinateSpace: frame.CoordinateSpace, Width: img.Bounds().Dx(), Height: img.Bounds().Dy(),
			Bytes: len(frame.Data), SHA256: hex.EncodeToString(digest[:]),
		}
		if err := consume(ctx, frame, metadata); err != nil {
			return result, err
		}
		result.Frames = append(result.Frames, VideoFrame{OffsetMS: offset.Milliseconds(), Metadata: metadata})
	}
	result.Complete = true
	return result, nil
}
