package inference_test

import (
	"errors"
	"testing"

	bridge "github.com/webong/ext/pkg/ext-go/inference"
	domain "github.com/webong/ext/res/int/inference"
)

func TestStructuredOutputRejectsInvalidShapeAndRefusal(t *testing.T) {
	response := domain.GenerationResponse{Model: "m", Message: domain.Message{Role: domain.RoleAssistant, Parts: []domain.Part{{Kind: domain.PartKindText, Text: `{"answer":3}`}}}, StopReason: domain.FinishReasonStop}
	schema := []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"integer"}},"additionalProperties":false}`)
	value, err := bridge.DecodeStructuredOutput[struct {
		Answer int `json:"answer"`
	}](response, schema)
	if err != nil || value.Answer != 3 {
		t.Fatalf("valid result: %+v, %v", value, err)
	}
	response.Message.Parts[0].Text = `{"answer":"wrong"}`
	if err := bridge.ValidateStructuredOutput(response, schema); !errors.Is(err, domain.ErrInvalidResponse) {
		t.Fatalf("schema mismatch accepted: %v", err)
	}
	response.Message.Parts[0].Text = `{"answer":3}`
	response.StopReason = domain.FinishReasonRefusal
	if err := bridge.ValidateStructuredOutput(response, schema); !errors.Is(err, domain.ErrInvalidResponse) {
		t.Fatalf("refusal accepted: %v", err)
	}
	response.StopReason = domain.FinishReasonStop
	if err := bridge.ValidateStructuredOutput(response, []byte(`{"unsupportedKeyword":true}`)); !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("invalid schema was not rejected as a request error: %v", err)
	}
}
