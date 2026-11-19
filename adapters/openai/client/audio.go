package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/webong/ext/res/int/inference"
)

// speechRequest is OpenAI's /audio/speech request.
type speechRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// marshalSpeechRequest encodes a speech request. OpenAI requires a voice; the request's
// options may not name fields this adapter does not map.
func marshalSpeechRequest(req inference.SpeechRequest) ([]byte, error) {
	if len(req.Options) != 0 && string(req.Options) != "{}" {
		return nil, fmt.Errorf("%w: speech options are not supported", inference.ErrUnsupported)
	}
	if strings.TrimSpace(req.Voice) == "" {
		return nil, fmt.Errorf("%w: voice is required", inference.ErrInvalidRequest)
	}
	return json.Marshal(speechRequest{Model: req.Model, Input: req.Input, Voice: req.Voice, ResponseFormat: req.Format})
}

// marshalTranscriptionRequest encodes a transcription request as OpenAI's multipart form,
// asking for verbose JSON so the response carries language, duration and timings.
func marshalTranscriptionRequest(req inference.TranscriptionRequest) ([]byte, string, error) {
	if len(req.Options) != 0 && string(req.Options) != "{}" {
		return nil, "", fmt.Errorf("%w: transcription options are not supported", inference.ErrUnsupported)
	}
	media, _, _ := mime.ParseMediaType(req.MediaType)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	field := func(name, value string) {
		if value != "" {
			_ = w.WriteField(name, value)
		}
	}
	field("model", req.Model)
	field("prompt", req.Prompt)
	field("language", req.Language)
	field("response_format", "verbose_json")
	for _, kind := range req.Timestamps {
		field("timestamp_granularities[]", kind)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="audio`+audioExtension(media)+`"`)
	header.Set("Content-Type", media)
	part, err := w.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(req.Audio); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// audioExtension names the uploaded file so OpenAI can tell its format.
func audioExtension(media string) string {
	switch media {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	default:
		return ""
	}
}

// transcriptionResponse is OpenAI's verbose JSON transcription.
type transcriptionResponse struct {
	Text     string   `json:"text"`
	Language string   `json:"language"`
	Duration *float64 `json:"duration"`
	Segments []struct {
		ID    int     `json:"id"`
		Text  string  `json:"text"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	} `json:"segments"`
	Words []struct {
		Word  string  `json:"word"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	} `json:"words"`
}

func parseTranscription(raw []byte) (inference.TranscriptionResponse, error) {
	var tr transcriptionResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return inference.TranscriptionResponse{}, inference.NewRemoteError(inference.ErrInvalidResponse, "failed to decode response", "openai")
	}
	resp := inference.TranscriptionResponse{Text: tr.Text, Language: tr.Language, DurationSeconds: tr.Duration}
	for _, s := range tr.Segments {
		resp.Segments = append(resp.Segments, inference.TranscriptionSegment{ID: s.ID, Text: s.Text, Start: s.Start, End: s.End})
	}
	for _, w := range tr.Words {
		resp.Words = append(resp.Words, inference.TranscriptionWord{Word: w.Word, Start: w.Start, End: w.End})
	}
	if err := resp.Validate(); err != nil {
		return inference.TranscriptionResponse{}, inference.NewRemoteError(inference.ErrInvalidResponse, "invalid transcription", "openai")
	}
	return resp, nil
}
