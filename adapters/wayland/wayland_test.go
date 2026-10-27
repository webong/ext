package wayland

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

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
		"wtype hello",
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
