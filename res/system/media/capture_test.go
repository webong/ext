package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"
)

func TestDifferenceCropAnnotateAndContactSheet(t *testing.T) {
	first := testFrame(t, color.RGBA{R: 10, A: 255})
	second := testFrame(t, color.RGBA{R: 10, A: 255})
	secondImage, err := png.Decode(bytes.NewReader(second.Data))
	if err != nil {
		t.Fatal(err)
	}
	modified := image.NewRGBA(secondImage.Bounds())
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			modified.Set(x, y, secondImage.At(x, y))
		}
	}
	modified.SetRGBA(1, 1, color.RGBA{B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, modified); err != nil {
		t.Fatal(err)
	}
	second.Data = encoded.Bytes()

	firstImage, _ := png.Decode(bytes.NewReader(first.Data))
	secondImage, _ = png.Decode(bytes.NewReader(second.Data))
	fraction, bounds := Difference(firstImage, secondImage)
	if fraction != 0.25 || bounds == nil || *bounds != (Region{X: 1, Y: 1, Width: 1, Height: 1}) {
		t.Fatalf("difference = %v, %#v", fraction, bounds)
	}

	cropped, err := Crop(second, Region{X: 1, Y: 1, Width: 1, Height: 1}, 1<<20, 16)
	if err != nil || cropped.CoordinateSpace != "crop-local-pixels" {
		t.Fatalf("crop = %#v, %v", cropped, err)
	}
	annotated, err := Annotate(first, []Annotation{{ID: "target", Region: Region{Width: 2, Height: 2}, Color: color.RGBA{G: 255, A: 255}}}, 1<<20, 16)
	if err != nil || len(annotated) == 0 {
		t.Fatalf("annotation bytes = %d, %v", len(annotated), err)
	}
	sheet, err := ContactSheet([]Frame{first, second}, ContactSheetOptions{
		Columns: 2, TileWidth: 2, TileHeight: 2,
		MaxFrameBytes: 1 << 20, MaxTotalBytes: 2 << 20, MaxPixels: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(sheet))
	if err != nil || config.Width != 4 || config.Height != 2 {
		t.Fatalf("contact sheet = %#v, %v", config, err)
	}
}

func TestSampleFiltersUnchangedFramesWithinBounds(t *testing.T) {
	frame := testFrame(t, color.RGBA{R: 10, A: 255})
	calls := 0
	source := FrameSourceFunc(func(context.Context) (Frame, error) {
		calls++
		frame.ObservedAt = time.Now().UTC()
		return frame, nil
	})
	manifest, err := Sample(context.Background(), source, SampleOptions{
		MaxFrames: 2, MaxDuration: time.Second, Interval: 20 * time.Millisecond,
		MaxFrameBytes: 1 << 20, MaxTotalBytes: 2 << 20, MaxPixels: 16, MinChange: 0.1,
	}, func(_ context.Context, _ Frame, metadata FrameMetadata) error {
		if metadata.SHA256 == "" {
			t.Fatal("retained frame is missing its digest")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || manifest.Sampled != 2 || manifest.Retained != 1 || !manifest.Complete ||
		manifest.StopReason != "frame_limit" || len(manifest.Frames) != 1 {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestSampleVideoRequiresStableTargetAndIncreasingOffsets(t *testing.T) {
	frame := testFrame(t, color.RGBA{A: 255})
	decoder := testVideoDecoder{frame: frame}
	options := VideoOptions{
		Offsets: []time.Duration{0, 10 * time.Millisecond}, MaxDuration: time.Second,
		MaxFrameBytes: 1 << 20, MaxTotalBytes: 2 << 20, MaxPixels: 16,
	}
	manifest, err := SampleVideo(context.Background(), decoder, options,
		func(context.Context, Frame, FrameMetadata) error { return nil })
	if err != nil || !manifest.Complete || len(manifest.Frames) != 2 {
		t.Fatalf("video manifest = %#v, %v", manifest, err)
	}
	options.Offsets = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	if _, err := SampleVideo(context.Background(), decoder, options,
		func(context.Context, Frame, FrameMetadata) error { return nil }); err == nil {
		t.Fatal("duplicate video offset was accepted")
	}
}

func TestAroundReturnsPostOperationEvidenceWithOperationError(t *testing.T) {
	frame := testFrame(t, color.RGBA{A: 255})
	calls := 0
	source := FrameSourceFunc(func(context.Context) (Frame, error) {
		calls++
		return frame, nil
	})
	want := context.Canceled
	before, after, err := Around(context.Background(), source, func(context.Context) error { return want })
	if calls != 2 || !bytes.Equal(before.Data, frame.Data) || !bytes.Equal(after.Data, frame.Data) || !errors.Is(err, want) {
		t.Fatalf("around = calls %d, before %#v, after %#v, error %v", calls, before, after, err)
	}
}

type testVideoDecoder struct{ frame Frame }

func (d testVideoDecoder) FrameAt(_ context.Context, offset time.Duration) (Frame, error) {
	frame := d.frame
	frame.ObservedAt = time.Unix(0, int64(offset)+1).UTC()
	return frame, nil
}

func testFrame(t *testing.T, fill color.RGBA) Frame {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return Frame{
		Data: data.Bytes(), ObservedAt: time.Now().UTC(),
		Target: "window:test", CoordinateSpace: "window-local-pixels",
	}
}
