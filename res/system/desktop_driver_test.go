package system

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/res/system/media"
)

type fakeDesktopDriver struct {
	name       string
	capture    SurfaceCapture
	captureErr error
	input      SurfaceInput
	inputErr   error
	recording  media.Recording
	recordErr  error
	options    media.RecordingOptions
}

func (f *fakeDesktopDriver) Name() string { return f.name }
func (f *fakeDesktopDriver) Capture(context.Context, CaptureOptions) (SurfaceCapture, error) {
	return f.capture, f.captureErr
}
func (f *fakeDesktopDriver) Input(_ context.Context, input SurfaceInput, _ InputOptions) error {
	f.input = input
	return f.inputErr
}
func (f *fakeDesktopDriver) Record(_ context.Context, options media.RecordingOptions) (media.Recording, error) {
	f.options = options
	return f.recording, f.recordErr
}

type fakeRecording struct{ closed bool }

func (f *fakeRecording) Read([]byte) (int, error)   { return 0, io.EOF }
func (f *fakeRecording) Close() error               { f.closed = true; return nil }
func (f *fakeRecording) Stop(context.Context) error { f.closed = true; return nil }

func TestCaptureDesktopValidatesThroughDriver(t *testing.T) {
	driver := &fakeDesktopDriver{name: "fixture", capture: SurfaceCapture{
		Format: "image/png", Width: 2, Height: 2, Data: []byte("png"),
	}}
	capture, err := CaptureDesktop(context.Background(), driver, CaptureOptions{})
	if err != nil || capture.Format != "image/png" {
		t.Fatalf("capture = %#v, %v", capture, err)
	}
	driver.capture = SurfaceCapture{Format: "image/png", Width: 0, Height: 2, Data: []byte("png")}
	if _, err = CaptureDesktop(context.Background(), driver, CaptureOptions{}); err == nil {
		t.Fatal("invalid capture was accepted")
	}
	driver.capture = SurfaceCapture{Format: "image/png", Width: 2, Height: 2, Data: make([]byte, 32)}
	if _, err = CaptureDesktop(context.Background(), driver, CaptureOptions{MaxBytes: 8}); err == nil {
		t.Fatal("oversized capture was accepted")
	}
	if _, err = CaptureDesktop(context.Background(), nil, CaptureOptions{}); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("nil driver = %v", err)
	}
	if _, err = CaptureDesktop(context.Background(), &fakeDesktopDriver{}, CaptureOptions{}); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("unnamed driver = %v", err)
	}
}

func TestSendDesktopInputValidatesOnce(t *testing.T) {
	driver := &fakeDesktopDriver{name: "fixture"}
	err := SendDesktopInput(context.Background(), driver, SurfaceInput{Kind: SurfaceInputText, Text: "hello"}, InputOptions{})
	if err != nil || driver.input.Text != "hello" {
		t.Fatalf("input = %#v, %v", driver.input, err)
	}
	if err = SendDesktopInput(context.Background(), driver, SurfaceInput{Kind: SurfaceInputText, Text: ""}, InputOptions{}); err == nil {
		t.Fatal("empty text was accepted")
	}
	if err = SendDesktopInput(context.Background(), driver, SurfaceInput{Kind: SurfaceInputText, Text: "long"}, InputOptions{MaxTextRunes: 2}); err == nil {
		t.Fatal("oversized text was accepted")
	}
	if err = SendDesktopInput(context.Background(), driver, SurfaceInput{Kind: "unknown"}, InputOptions{}); err == nil {
		t.Fatal("unknown input kind was accepted")
	}
}

func TestRecordDesktopValidatesOptionsAndStream(t *testing.T) {
	recording := &fakeRecording{}
	driver := &fakeDesktopDriver{name: "fixture", recording: recording}
	result, err := RecordDesktop(context.Background(), driver, media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 1024})
	if err != nil || result == nil || driver.options.Format != "webm" {
		t.Fatalf("recording = %#v, %#v, %v", result, driver.options, err)
	}
	if _, err = RecordDesktop(context.Background(), driver, media.RecordingOptions{Format: "avi"}); !errors.Is(err, ErrInvalidRecording) {
		t.Fatalf("unsupported format = %v", err)
	}
	driver.recordErr = errors.New("no display")
	if _, err = RecordDesktop(context.Background(), driver, media.RecordingOptions{Format: "webm"}); err == nil || !strings.Contains(err.Error(), "no display") {
		t.Fatalf("driver error = %v", err)
	}
	driver.recordErr = nil
	driver.recording = nil
	if _, err = RecordDesktop(context.Background(), driver, media.RecordingOptions{Format: "webm"}); err == nil {
		t.Fatal("nil recording was accepted")
	}
}
