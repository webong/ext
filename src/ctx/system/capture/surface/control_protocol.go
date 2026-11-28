package surface

import (
	"errors"
	"time"
)

// ControlProtocol is the wire version of the native surface resource guest.
const ControlProtocol = "ext.system.surface/v1"

// CaptureRequest asks a native guest to capture one observed window. The guest
// must revalidate the window ID, process generation, and geometry before use.
type CaptureRequest struct {
	Target    WindowInfo `json:"target"`
	MaxPixels int        `json:"max_pixels"`
	MaxBytes  int        `json:"max_bytes"`
	TimeoutMS int64      `json:"timeout_ms"`
}

// NewCaptureRequest applies the public capture bounds before crossing the
// guest boundary. The process identity check is performed by the guest.
func NewCaptureRequest(target WindowInfo, options CaptureOptions) (CaptureRequest, error) {
	if err := validateSurfaceTarget(target); err != nil {
		return CaptureRequest{}, err
	}
	options, err := captureOptions(options)
	if err != nil {
		return CaptureRequest{}, err
	}
	request := CaptureRequest{Target: target, MaxPixels: options.MaxPixels, MaxBytes: options.MaxBytes, TimeoutMS: options.Timeout.Milliseconds()}
	return request, request.Validate()
}

// InputRequest asks a native guest to perform one atomic operation on an
// observed window. The guest must revalidate identity, geometry, and focus.
type InputRequest struct {
	Target       WindowInfo   `json:"target"`
	Input        SurfaceInput `json:"input"`
	MaxTextRunes int          `json:"max_text_runes"`
	TimeoutMS    int64        `json:"timeout_ms"`
}

// NewInputRequest applies the public input bounds before crossing the guest
// boundary. The guest independently checks them before native input.
func NewInputRequest(target WindowInfo, input SurfaceInput, options InputOptions) (InputRequest, error) {
	if err := validateSurfaceTarget(target); err != nil {
		return InputRequest{}, err
	}
	options, err := inputOptions(options)
	if err != nil {
		return InputRequest{}, err
	}
	input, err = validateSurfaceInput(input, options)
	if err != nil {
		return InputRequest{}, err
	}
	request := InputRequest{Target: target, Input: input, MaxTextRunes: options.MaxTextRunes, TimeoutMS: options.Timeout.Milliseconds()}
	return request, request.Validate()
}

// Validate checks the guest-facing bounds, including a positive deadline.
func (request CaptureRequest) Validate() error {
	if err := validateSurfaceTarget(request.Target); err != nil {
		return err
	}
	if request.MaxPixels < 1 || request.MaxPixels > 268_435_456 || request.MaxBytes < 1 || request.MaxBytes > 1<<30 || request.TimeoutMS < 1 || request.TimeoutMS > time.Hour.Milliseconds() {
		return errors.New("invalid surface capture limits")
	}
	return nil
}

// Validate checks the guest-facing input bounds and operation.
func (request InputRequest) Validate() error {
	if err := validateSurfaceTarget(request.Target); err != nil {
		return err
	}
	if request.MaxTextRunes < 1 || request.MaxTextRunes > 1_000_000 || request.TimeoutMS < 1 || request.TimeoutMS > time.Hour.Milliseconds() {
		return errors.New("invalid surface input limits")
	}
	_, err := validateSurfaceInput(request.Input, InputOptions{MaxTextRunes: request.MaxTextRunes, Timeout: time.Duration(request.TimeoutMS) * time.Millisecond})
	return err
}

// ValidateCapture checks the native guest's encoded result against the request.
func (request CaptureRequest) ValidateCapture(result SurfaceCapture) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if result.Width <= 0 || result.Height <= 0 || result.Format != "image/png" || len(result.Data) == 0 || result.CapturedAt.IsZero() {
		return errors.New("invalid surface capture response")
	}
	if result.Width > request.MaxPixels/result.Height || len(result.Data) > request.MaxBytes {
		return errors.New("surface capture exceeded requested limits")
	}
	return nil
}
