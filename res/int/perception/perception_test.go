package perception

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestInterpretationProvenance(t *testing.T) {
	request := InterpretRequest{APIVersion: APIVersion, RequestID: "r1", Model: "m", Parts: []Part{{Kind: "text", Text: "hello"}, {Kind: "image", MediaType: "image/png", Data: []byte{1}}}}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	response := InterpretResponse{APIVersion: APIVersion, RequestID: "r1", Findings: []Finding{{Kind: "summary", Text: "hello", SourceParts: []int{0, 1}}}}
	if err := response.Validate(request); err != nil {
		t.Fatal(err)
	}
	response.Findings[0].SourceParts = []int{0, 0}
	if err := response.Validate(request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate source accepted: %v", err)
	}
	response.Findings[0].SourceParts = []int{0}
	invalid := math.NaN()
	response.Findings[0].Confidence = &invalid
	if err := response.Validate(request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nonfinite confidence accepted: %v", err)
	}
}

func TestObservationBounds(t *testing.T) {
	request := ObserveRequest{APIVersion: APIVersion, Model: "m", MediaType: "image/png", Image: []byte{1}}
	response := ObserveResponse{APIVersion: APIVersion, Observations: []Observation{{Kind: "object", Label: "cat", Confidence: 0.9, Region: Region{Width: 3, Height: 2}}}}
	if err := response.Validate(request); err != nil {
		t.Fatal(err)
	}
	response.Observations[0].Region.X = math.Inf(1)
	if err := response.Validate(request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("infinite region accepted: %v", err)
	}
	response.Observations[0].Region.X = 0
	response.Observations[0].Mask = "not-png"
	if err := response.Validate(request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid mask accepted: %v", err)
	}
	if _, err := json.Marshal(response); err != nil {
		t.Fatal(err)
	}
}
