// Package inference transcription types define transcription requests and
// responses. Timestamps carry per-segment or per-word timing.
package inference

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"strings"
)

// TranscriptionRequest requests transcription of audio.
type TranscriptionRequest struct {
	// Model is the selected transcription model.
	Model string `json:"model"`
	// Audio is the audio bytes to transcribe.
	Audio []byte `json:"audio,omitempty"`
	// MediaType is the MIME type of the audio (must be audio/*).
	MediaType string `json:"mediaType,omitempty"`
	// Prompt is an optional transcription prompt.
	Prompt string `json:"prompt,omitempty"`
	// Language is the language code, where supported.
	Language string `json:"language,omitempty"`
	// Timestamps selects "word" and/or "segment" timing.
	Timestamps []string `json:"timestamps,omitempty"`
	// Options are provider-owned options.
	Options json.RawMessage `json:"options,omitempty"`
	// Limits are explicit bounds for this request.
	Limits Limits `json:"limits,omitempty"`
}

// Validate checks the request.
func (r TranscriptionRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	media, _, err := mime.ParseMediaType(r.MediaType)
	if err != nil || !strings.HasPrefix(media, "audio/") {
		return fmt.Errorf("%w: media type must be audio/*", ErrInvalidRequest)
	}
	if len(r.Audio) == 0 || len(r.Audio) > 64<<20 {
		return fmt.Errorf("%w: audio must be between 1 and 64 MiB", ErrInvalidRequest)
	}
	if len(r.Prompt) > 1<<20 || len(r.Language) > 128 {
		return fmt.Errorf("%w: prompt or language too long", ErrInvalidRequest)
	}
	seen := map[string]bool{}
	for _, kind := range r.Timestamps {
		if kind != "word" && kind != "segment" || seen[kind] {
			return fmt.Errorf("%w: invalid timestamp selection %q", ErrInvalidRequest, kind)
		}
		seen[kind] = true
	}
	if len(r.Timestamps) > 2 {
		return fmt.Errorf("%w: at most two timestamp kinds", ErrInvalidRequest)
	}
	if err := r.Limits.Validate(); err != nil {
		return err
	}
	if len(r.Options) != 0 && !jsonObject(r.Options) {
		return fmt.Errorf("%w: adapter options must be a JSON object", ErrInvalidRequest)
	}
	return nil
}

// TranscriptionSegment is one transcribed segment with timing in seconds.
type TranscriptionSegment struct {
	// ID is the segment index.
	ID int `json:"id"`
	// Text is the transcribed text.
	Text string `json:"text"`
	// Start is the start time in seconds.
	Start float64 `json:"start"`
	// End is the end time in seconds.
	End float64 `json:"end"`
}

// TranscriptionWord is one transcribed word with timing in seconds.
type TranscriptionWord struct {
	// Word is the transcribed word.
	Word string `json:"word"`
	// Start is the start time in seconds.
	Start float64 `json:"start"`
	// End is the end time in seconds.
	End float64 `json:"end"`
}

// TranscriptionResponse is the result of a transcription call.
type TranscriptionResponse struct {
	// Text is the full transcription.
	Text string `json:"text"`
	// Language is the detected or requested language.
	Language string `json:"language,omitempty"`
	// DurationSeconds is the audio duration in seconds.
	DurationSeconds *float64 `json:"durationSeconds,omitempty"`
	// Segments are per-segment transcriptions.
	Segments []TranscriptionSegment `json:"segments,omitempty"`
	// Words are per-word transcriptions.
	Words []TranscriptionWord `json:"words,omitempty"`
}

// Validate checks the response.
func (r TranscriptionResponse) Validate() error {
	finite := func(f float64) bool { return !isNaN(f) && !isInf(f) && f >= 0 }
	if len(r.Text) > 32<<20 || len(r.Language) > 128 {
		return fmt.Errorf("%w: transcription metadata too long", ErrInvalidResponse)
	}
	if r.DurationSeconds != nil && !finite(*r.DurationSeconds) {
		return fmt.Errorf("%w: duration must be non-negative", ErrInvalidResponse)
	}
	var bytes int
	for _, s := range r.Segments {
		bytes += len(s.Text)
		if s.ID < 0 || !finite(s.Start) || !finite(s.End) || s.End < s.Start {
			return fmt.Errorf("%w: invalid segment timing", ErrInvalidResponse)
		}
	}
	for _, w := range r.Words {
		bytes += len(w.Word)
		if !finite(w.Start) || !finite(w.End) || w.End < w.Start {
			return fmt.Errorf("%w: invalid word timing", ErrInvalidResponse)
		}
	}
	if bytes > 32<<20 {
		return fmt.Errorf("%w: transcription metadata exceeds byte ceiling", ErrInvalidResponse)
	}
	if len(r.Segments) > 100000 || len(r.Words) > 100000 {
		return fmt.Errorf("%w: too many segments or words", ErrInvalidResponse)
	}
	return nil
}

func isNaN(f float64) bool { return f != f }
func isInf(f float64) bool { return math.IsInf(f, 0) }

var _ = errors.Unwrap
