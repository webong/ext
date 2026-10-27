package x11

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/res/system"
	"github.com/webong/ext/res/system/media"
)

func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type commandCall struct{ executable, arguments string }

type fakeRunner struct {
	calls  []commandCall
	output []byte
	failed bool
}

func (f *fakeRunner) run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	f.calls = append(f.calls, commandCall{name, strings.Join(arguments, " ")})
	if f.failed {
		return nil, errors.New("tool failed")
	}
	return f.output, nil
}

func TestCaptureUsesImportAndBounds(t *testing.T) {
	runner := &fakeRunner{output: pngFixture(t, 8, 6)}
	driver := New(Config{Run: runner.run})
	capture, err := driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 1024, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Format != "image/png" || capture.Width != 8 || capture.Height != 6 || capture.CapturedAt.IsZero() {
		t.Fatalf("capture = %#v", capture)
	}
	if len(runner.calls) != 1 || runner.calls[0].executable != "import" ||
		runner.calls[0].arguments != "-window root png:-" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	if _, err = driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 16, MaxBytes: 1 << 20}); err == nil {
		t.Fatal("pixel bound was not enforced")
	}
	if _, err = driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 1024, MaxBytes: 8}); err == nil {
		t.Fatal("byte bound was not enforced")
	}
	runner.failed = true
	if _, err = driver.Capture(context.Background(), system.CaptureOptions{}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("tool failure = %v", err)
	}
}

func TestInputMapsOperationsToXdotool(t *testing.T) {
	runner := &fakeRunner{}
	driver := New(Config{Run: runner.run})
	options := system.InputOptions{MaxTextRunes: 128}
	if err := driver.Input(context.Background(), system.SurfaceInput{Kind: system.SurfaceInputText, Text: "hello"}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputKey, Key: "t", Modifiers: []string{"command", "shift"},
	}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputClick, Point: system.SurfacePoint{X: 3, Y: 4}, Button: "right",
	}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputDrag, Point: system.SurfacePoint{X: 1, Y: 2}, End: system.SurfacePoint{X: 5, Y: 6},
	}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputScroll, Point: system.SurfacePoint{X: 0, Y: 0}, DeltaY: -360,
	}, options); err != nil {
		t.Fatal(err)
	}
	values := []string{}
	for _, call := range runner.calls {
		values = append(values, call.arguments)
	}
	joined := strings.Join(values, "\n")
	for _, fragment := range []string{
		"type --clearmodifiers hello",
		"key --clearmodifiers super+shift+t",
		"mousemove 3 4",
		"click 3",
		"mousedown 1",
		"mousemove 5 6",
		"mouseup 1",
		"click 4",
	} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("input commands are missing %q:\n%s", fragment, joined)
		}
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{Kind: "unknown"}, options); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("unknown input = %v", err)
	}
}

func TestScrollIsBounded(t *testing.T) {
	runner := &fakeRunner{}
	driver := New(Config{Run: runner.run})
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputScroll, DeltaY: -100_000,
	}, system.InputOptions{}); err != nil {
		t.Fatal(err)
	}
	clicks := 0
	for _, call := range runner.calls {
		if strings.HasPrefix(call.arguments, "click") {
			clicks++
		}
	}
	if clicks != 20 {
		t.Fatalf("scroll clicks = %d", clicks)
	}
}

func TestRecordRequiresDisplayAndBuildsBoundedFfmpeg(t *testing.T) {
	runner := &fakeRunner{}
	starter := &fakeStarter{output: bytes.NewBuffer(nil)}
	driver := New(Config{Run: runner.run, Start: starter.start})
	t.Setenv("DISPLAY", "")
	if _, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 1024}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("missing DISPLAY = %v", err)
	}
	t.Setenv("DISPLAY", ":1")
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(starter.arguments, " ")
	for _, fragment := range []string{"-f x11grab", "-framerate 30", "-i :1", "-t 60", "-fs 1024", "libvpx-vp9", "-f webm", "pipe:1"} {
		if !strings.Contains(arguments, fragment) {
			t.Errorf("ffmpeg arguments are missing %q: %s", fragment, arguments)
		}
	}
	if err = recording.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !starter.process.killed {
		t.Fatal("recording process was not stopped")
	}
	t.Setenv("DISPLAY", ":2")
	if _, err = driver.Record(context.Background(), media.RecordingOptions{Format: "mp4", FrameRate: 60, MaxDuration: time.Minute, MaxBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(starter.arguments, " "), "libx264") {
		t.Fatalf("mp4 recording did not select libx264: %#v", starter.arguments)
	}
}

type fakeStarter struct {
	arguments []string
	process   *fakeProcess
	output    *bytes.Buffer
}

func (f *fakeStarter) start(_ context.Context, name string, arguments ...string) (Process, error) {
	f.arguments = append([]string{name}, arguments...)
	f.process = &fakeProcess{output: f.output}
	return f.process, nil
}

type fakeProcess struct {
	output *bytes.Buffer
	killed bool
}

func (f *fakeProcess) Stdout() ioReadCloser { return nopReadCloser{f.output} }
func (f *fakeProcess) Wait() error          { return nil }
func (f *fakeProcess) Kill() error          { f.killed = true; return nil }

type nopReadCloser struct{ *bytes.Buffer }

func (nopReadCloser) Close() error { return nil }
