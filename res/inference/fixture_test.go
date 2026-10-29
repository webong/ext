// Package inference_test validates shared fixtures against the Go types and
// their Validate methods. The JSON schemas under testdata/schemas/ are the
// language-neutral contract; this test provides Go coverage of the same shapes.
package inference_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/res/inference"
)

func fixturePath(name string) string {
	return filepath.Join("testdata", "fixtures", name)
}

func loadJSON(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return data
}

// unmarshalAndValidate decodes a fixture into v and runs its Validate method.
func unmarshalAndValidate(t *testing.T, name string, v any) {
	t.Helper()
	data := loadJSON(t, name)
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %q: %v", name, err)
	}
	if validator, ok := v.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			t.Fatalf("validate %q: %v", name, err)
		}
	}
}

// roundTrip decodes a fixture into the typed value, re-marshals it, and
// decodes again to confirm the type round-trips without error. It does not
// require byte-identical output because omitempty drops zero-value fields.
func roundTrip(t *testing.T, name string, v any) {
	t.Helper()
	data := loadJSON(t, name)
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal into typed %q: %v", name, err)
	}
	reMarshal, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("remarshal %q: %v", name, err)
	}
	var again any
	if err := json.Unmarshal(reMarshal, &again); err != nil {
		t.Fatalf("unmarshal remarshaled %q: %v", name, err)
	}
	_ = again
}

func TestProviderInfoFixtures(t *testing.T) {
	for _, name := range []string{"provider-info-openai.json", "provider-info-coreml.json"} {
		var info inference.ProviderInfo
		roundTrip(t, name, &info)
		unmarshalAndValidate(t, name, &info)
	}
}

func TestGenerationRequestFixtures(t *testing.T) {
	for _, name := range []string{"generation-request-multimodal.json", "generation-request-tools.json"} {
		var req inference.GenerationRequest
		roundTrip(t, name, &req)
		unmarshalAndValidate(t, name, &req)
	}
}

func TestGenerationResponseFixtures(t *testing.T) {
	for _, name := range []string{"generation-response-tools.json", "generation-response-structured.json", "generation-response-refusal.json"} {
		var resp inference.GenerationResponse
		roundTrip(t, name, &resp)
		unmarshalAndValidate(t, name, &resp)
	}
}

func TestEmbeddingFixtures(t *testing.T) {
	var req inference.EmbeddingRequest
	roundTrip(t, "embedding-request.json", &req)
	unmarshalAndValidate(t, "embedding-request.json", &req)
	var resp inference.EmbeddingResponse
	roundTrip(t, "embedding-response.json", &resp)
	unmarshalAndValidate(t, "embedding-response.json", &resp)
}

func TestTranscriptionFixtures(t *testing.T) {
	var req inference.TranscriptionRequest
	roundTrip(t, "transcription-request.json", &req)
	unmarshalAndValidate(t, "transcription-request.json", &req)
	var resp inference.TranscriptionResponse
	roundTrip(t, "transcription-response.json", &resp)
	unmarshalAndValidate(t, "transcription-response.json", &resp)
}

func TestSpeechRequestFixture(t *testing.T) {
	var req inference.SpeechRequest
	roundTrip(t, "speech-request.json", &req)
	unmarshalAndValidate(t, "speech-request.json", &req)
}

func TestTypedErrors(t *testing.T) {
	data := loadJSON(t, "typed-errors.json")
	var errs []inference.RemoteError
	if err := json.Unmarshal(data, &errs); err != nil {
		t.Fatalf("unmarshal typed errors: %v", err)
	}
	expected := []string{"authentication", "rateLimit", "unavailable", "unsupported", "invalidRequest", "resourceExhausted", "incompatibleModel", "transientNative"}
	if len(errs) != len(expected) {
		t.Fatalf("expected %d errors, got %d", len(expected), len(errs))
	}
	for i, e := range errs {
		if e.Code != expected[i] {
			t.Fatalf("error %d code = %q, want %q", i, e.Code, expected[i])
		}
		if len(e.Message) > 4096 {
			t.Fatalf("error message too long: %q", e.Message)
		}
		if !errorsIs(&e) {
			t.Fatalf("error %d does not match a sentinel: %q", i, e.Code)
		}
	}
}

func errorsIs(e *inference.RemoteError) bool {
	switch e.Code {
	case "authentication":
		return errors.Is(e, inference.ErrAuthentication)
	case "rateLimit":
		return errors.Is(e, inference.ErrRateLimit)
	case "unavailable":
		return errors.Is(e, inference.ErrUnavailable)
	case "unsupported":
		return errors.Is(e, inference.ErrUnsupported)
	case "invalidRequest":
		return errors.Is(e, inference.ErrInvalidRequest)
	case "resourceExhausted":
		return errors.Is(e, inference.ErrResourceExhausted)
	case "incompatibleModel":
		return errors.Is(e, inference.ErrIncompatibleModel)
	case "transientNative":
		return errors.Is(e, inference.ErrTransientNative)
	default:
		return false
	}
}

func TestMalformedLimitsRejected(t *testing.T) {
	req := inference.GenerationRequest{
		Model:    "gpt-4o",
		Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hi"}}}},
		Limits:   inference.Limits{MaxOutputTokens: -1},
	}
	if err := req.Validate(); err == nil {
		t.Fatal("expected validation error for negative maxOutputTokens")
	} else if !strings.Contains(err.Error(), "maxOutputTokens") {
		t.Fatalf("expected maxOutputTokens error, got %v", err)
	}
}

func TestModelListFixture(t *testing.T) {
	data := loadJSON(t, "model-list.json")
	var models []inference.ModelInfo
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatalf("unmarshal model list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	for _, m := range models {
		if m.ID == "" {
			t.Fatal("model missing id")
		}
	}
}
