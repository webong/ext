// Package inference speech types define speech-synthesis requests and the
// bounded byte stream of synthesized audio.
package inference

import (
	"encoding/json"
	"fmt"
	"mime"
	"strings"
)

// SpeechRequest requests speech synthesis of text.
type SpeechRequest struct {
	// Model is the selected synthesis model.
	Model string `json:"model"`
	// Input is the text to synthesize.
	Input string `json:"input"`
	// Voice is the voice identifier, where supported.
	Voice string `json:"voice,omitempty"`
	// Format is the output audio format (for example "mp3", "wav", "opus").
	Format string `json:"format,omitempty"`
	// Options are provider-owned options.
	Options json.RawMessage `json:"options,omitempty"`
	// Limits are explicit bounds for this request.
	Limits Limits `json:"limits,omitempty"`
}

// Validate checks the request.
func (r SpeechRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.Input) == "" {
		return fmt.Errorf("%w: input is required", ErrInvalidRequest)
	}
	if len(r.Input) > 32<<20 {
		return fmt.Errorf("%w: input exceeds 32 MiB", ErrInvalidRequest)
	}
	if r.Format != "" {
		if _, _, err := mime.ParseMediaType("audio/" + r.Format); err != nil {
			return fmt.Errorf("%w: unsupported audio format %q", ErrInvalidRequest, r.Format)
		}
	}
	if err := r.Limits.Validate(); err != nil {
		return err
	}
	if len(r.Options) != 0 && !jsonObject(r.Options) {
		return fmt.Errorf("%w: adapter options must be a JSON object", ErrInvalidRequest)
	}
	return nil
}

// SpeechStream is a bounded stream of synthesized audio bytes. Close releases
// the stream and any underlying audio resources.
type SpeechStream interface {
	// Read reads up to len(p) audio bytes. It returns io.EOF when the stream
	// is complete. Cancellation or an error aborts the stream.
	Read(p []byte) (int, error)
	// Close releases the stream. It is safe to call after an error and is
	// idempotent.
	Close() error
}
