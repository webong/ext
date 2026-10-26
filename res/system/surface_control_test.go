package system

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestCaptureAndInputOptions(t *testing.T) {
	capture, err := captureOptions(CaptureOptions{})
	if err != nil || capture.MaxPixels != 16_777_216 || capture.MaxBytes != 16<<20 || capture.Timeout <= 0 {
		t.Fatalf("capture options = %#v, %v", capture, err)
	}
	input, err := inputOptions(InputOptions{})
	if err != nil || input.MaxTextRunes != 4096 || input.Timeout <= 0 {
		t.Fatalf("input options = %#v, %v", input, err)
	}
	if _, err := captureOptions(CaptureOptions{MaxBytes: -1}); err == nil {
		t.Fatal("negative capture limit was accepted")
	}
	if _, err := inputOptions(InputOptions{MaxTextRunes: -1}); err == nil {
		t.Fatal("negative input limit was accepted")
	}
}

func TestValidateSurfaceInput(t *testing.T) {
	options, _ := inputOptions(InputOptions{MaxTextRunes: 4})
	click, err := validateSurfaceInput(SurfaceInput{Kind: SurfaceInputClick}, options)
	if err != nil || click.Button != "left" {
		t.Fatalf("default click = %#v, %v", click, err)
	}
	for name, input := range map[string]SurfaceInput{
		"negative point": {Kind: SurfaceInputMove, Point: SurfacePoint{X: -1}},
		"bad button":     {Kind: SurfaceInputClick, Button: "side"},
		"long text":      {Kind: SurfaceInputText, Text: "hello"},
		"bad modifier":   {Kind: SurfaceInputKey, Key: "enter", Modifiers: []string{"meta"}},
		"duplicate":      {Kind: SurfaceInputKey, Key: "enter", Modifiers: []string{"shift", "SHIFT"}},
		"unknown":        {Kind: "launch"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateSurfaceInput(input, options); err == nil {
				t.Fatalf("input %#v was accepted", input)
			}
		})
	}
}

func TestSurfaceOperationsRejectUnobservedTargets(t *testing.T) {
	target := WindowInfo{ID: "window:fixture", PID: 1, ProcessStartID: "generation", Bounds: SurfaceBounds{Width: 1, Height: 1}}
	if _, err := CaptureWindow(context.Background(), WindowInfo{}, CaptureOptions{}); err == nil {
		t.Fatal("invalid capture target was accepted")
	}
	if err := SendWindowInput(context.Background(), WindowInfo{}, SurfaceInput{Kind: SurfaceInputMove}, InputOptions{}); err == nil {
		t.Fatal("invalid input target was accepted")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		_, err := CaptureWindow(context.Background(), target, CaptureOptions{})
		if !errors.Is(err, ErrSurfaceUnavailable) {
			t.Fatalf("unsupported capture error = %v", err)
		}
	}
}

func TestCaptureJSONUsesBytesNotTransportEncoding(t *testing.T) {
	encoded, err := json.Marshal(SurfaceCapture{Format: "image/png", Width: 1, Height: 1, Data: []byte{0}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "base64_data") {
		t.Fatal("capture exposed a transport-specific field")
	}
}
