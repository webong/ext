package wayland

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
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

type fakeRunner struct {
	calls  []string
	output []byte
	failed bool
}

func (f *fakeRunner) run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(arguments, " "))
	if f.failed {
		return nil, errors.New("tool failed")
	}
	return f.output, nil
}

func TestCaptureUsesGrimAndBounds(t *testing.T) {
	runner := &fakeRunner{output: pngFixture(t, 4, 3)}
	driver := New(Config{Run: runner.run})
	capture, err := driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 1024, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Width != 4 || capture.Height != 3 || capture.Format != "image/png" {
		t.Fatalf("capture = %#v", capture)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "grim -" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	if _, err = driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 4, MaxBytes: 1 << 20}); err == nil {
		t.Fatal("pixel bound was not enforced")
	}
	runner.failed = true
	if _, err = driver.Capture(context.Background(), system.CaptureOptions{}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("tool failure = %v", err)
	}
}

func TestCaptureSelectsOutput(t *testing.T) {
	runner := &fakeRunner{output: pngFixture(t, 4, 3)}
	driver := New(Config{Run: runner.run, Output: "HDMI-A-1"})
	capture, err := driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 1024, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Width != 4 || capture.Height != 3 {
		t.Fatalf("capture = %#v", capture)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "grim -o HDMI-A-1 -" {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestInputUsesWtypeAndRefusesPointer(t *testing.T) {
	runner := &fakeRunner{}
	driver := New(Config{Run: runner.run})
	if err := driver.Input(context.Background(), system.SurfaceInput{Kind: system.SurfaceInputText, Text: "hello"}, system.InputOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputKey, Key: "enter", Modifiers: []string{"command", "shift"},
	}, system.InputOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputKey, Key: "backspace",
	}, system.InputOptions{}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, fragment := range []string{
		"wtype -- hello",
		"wtype -M logo -M shift -k Return -m logo -m shift",
		"wtype -k BackSpace",
	} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("input calls are missing %q:\n%s", fragment, joined)
		}
	}
	for _, kind := range []system.SurfaceInputKind{
		system.SurfaceInputMove, system.SurfaceInputClick, system.SurfaceInputDrag, system.SurfaceInputScroll,
	} {
		if err := driver.Input(context.Background(), system.SurfaceInput{Kind: kind}, system.InputOptions{}); !errors.Is(err, system.ErrDesktopUnavailable) {
			t.Errorf("%s = %v", kind, err)
		}
	}
}

func TestRecordingIsExplicitlyUnavailable(t *testing.T) {
	driver := New(Config{})
	if _, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm"}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("wayland recording = %v", err)
	}
	if !strings.Contains(driver.Name(), "wayland") {
		t.Fatalf("name = %q", driver.Name())
	}
}

func TestRecordingRequiresExplicitRecorder(t *testing.T) {
	driver := New(Config{Run: (&fakeRunner{}).run})
	if _, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm"}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("wayland recording without recorder = %v", err)
	}
}

func TestRecordingUsesConfiguredRecorder(t *testing.T) {
	runner := &fakeRunner{output: []byte("webm-frames")}
	process := &fakeProcess{out: bytes.NewBuffer(nil), in: bytes.NewBuffer(nil)}
	driver := New(Config{Run: runner.run, Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			runner.calls = append(runner.calls, name+" "+strings.Join(arguments, " "))
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, " ")
	for _, fragment := range []string{"wf-recorder --fps 30 --codec webm --file -"} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("recording arguments are missing %q:\n%s", fragment, joined)
		}
	}
	if err := recording.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := recording.Read(make([]byte, 16)); err != nil && err != io.EOF {
		t.Fatalf("read = %v", err)
	}
}

func TestConfiguredYdotoolHandlesPointerInput(t *testing.T) {
	runner := &fakeRunner{}
	driver := New(Config{Run: runner.run, Pointer: "ydotool"})
	options := system.InputOptions{}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputMove, Point: system.SurfacePoint{X: 10, Y: 20},
	}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputClick, Point: system.SurfacePoint{X: 1, Y: 2}, Button: "right",
	}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputDrag, Point: system.SurfacePoint{X: 3, Y: 4}, End: system.SurfacePoint{X: 5, Y: 6},
	}, options); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, fragment := range []string{
		"ydotool mousemove --absolute 10 20",
		"ydotool click 0xC1",
		"ydotool click 0x40",
		"ydotool mousemove --absolute 5 6",
		"ydotool click 0x80",
	} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("pointer calls are missing %q:\n%s", fragment, joined)
		}
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputScroll, Point: system.SurfacePoint{X: 0, Y: 0}, DeltaY: -120,
	}, options); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("scroll = %v", err)
	}
}

func TestPointerRequiresExplicitDriver(t *testing.T) {
	driver := New(Config{})
	err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputMove, Point: system.SurfacePoint{X: 1, Y: 1},
	}, system.InputOptions{})
	if !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("unconfigured pointer = %v", err)
	}
}

func TestYdotoolDragReleasesTheButtonOnFailure(t *testing.T) {
	calls := []string{}
	driver := New(Config{
		Run: func(_ context.Context, name string, arguments ...string) ([]byte, error) {
			value := name + " " + strings.Join(arguments, " ")
			calls = append(calls, value)
			if strings.Contains(value, "mousemove --absolute 5 6") {
				return nil, errors.New("move failed")
			}
			return nil, nil
		},
		Pointer: "ydotool",
	})
	err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputDrag, Point: system.SurfacePoint{X: 3, Y: 4}, End: system.SurfacePoint{X: 5, Y: 6},
	}, system.InputOptions{})
	if !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("failed drag = %v", err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "ydotool click 0x80") {
		t.Fatalf("drag did not release the button:\n%s", joined)
	}
}

type fakeProcess struct {
	out *bytes.Buffer
	in  *bytes.Buffer
}

func (f *fakeProcess) Stdout() io.ReadCloser { return io.NopCloser(f.out) }
func (f *fakeProcess) Stdin() io.WriteCloser { return nopWriteCloser{Writer: f.in} }
func (f *fakeProcess) Wait() error           { return nil }
func (f *fakeProcess) Kill() error           { return nil }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
