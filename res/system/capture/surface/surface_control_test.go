package surface

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestSurfaceGuestRequestsRejectUnobservedTargets(t *testing.T) {
	target := WindowInfo{ID: "window:fixture", PID: 1, ProcessStartID: "generation", Bounds: SurfaceBounds{Width: 1, Height: 1}}
	if _, err := NewCaptureRequest(WindowInfo{}, CaptureOptions{}); err == nil {
		t.Fatal("invalid capture target was accepted")
	}
	if _, err := NewInputRequest(WindowInfo{}, SurfaceInput{Kind: SurfaceInputMove}, InputOptions{}); err == nil {
		t.Fatal("invalid input target was accepted")
	}
	capture, err := NewCaptureRequest(target, CaptureOptions{})
	if err != nil || capture.MaxBytes != 16<<20 || capture.TimeoutMS != 5000 {
		t.Fatalf("capture request = %+v, %v", capture, err)
	}
	if err := capture.ValidateCapture(SurfaceCapture{Format: "image/png", Width: 1, Height: 1, Data: []byte{1}, CapturedAt: time.Now()}); err != nil {
		t.Fatalf("valid guest capture = %v", err)
	}
	if err := capture.ValidateCapture(SurfaceCapture{Format: "image/png", Width: 1, Height: 1, Data: []byte{1}}); err == nil {
		t.Fatal("capture without timestamp was accepted")
	}
	input, err := NewInputRequest(target, SurfaceInput{Kind: SurfaceInputText, Text: "x"}, InputOptions{})
	if err != nil || input.MaxTextRunes != 4096 || input.TimeoutMS != 5000 {
		t.Fatalf("input request = %+v, %v", input, err)
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

func TestSurfaceControlWireFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "surface", "v1", "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Capture CaptureRequest `json:"capture"`
		Input   InputRequest   `json:"input"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if err := cases.Capture.Validate(); err != nil {
		t.Fatalf("capture fixture: %v", err)
	}
	if err := cases.Input.Validate(); err != nil {
		t.Fatalf("input fixture: %v", err)
	}
}
