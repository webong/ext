package inference

import (
	"encoding/json"
	"fmt"
)

// SchemaValidator checks one JSON value against a caller-supplied JSON Schema.
// Injection keeps the portable inference module independent of a particular
// schema engine while requiring real validation before typed decoding.
type SchemaValidator func(schema, value json.RawMessage) error

// ValidateStructuredOutput checks that a completed response is a schema-valid
// JSON object. Refusals, truncation, and tool proposals are never objects.
func ValidateStructuredOutput(response GenerationResponse, schema json.RawMessage, validate SchemaValidator) error {
	if validate == nil || len(schema) == 0 || len(schema) > 1<<20 || !json.Valid(schema) {
		return fmt.Errorf("%w: schema validator and valid schema are required", ErrInvalidRequest)
	}
	if err := response.Validate(); err != nil {
		return err
	}
	if response.StopReason != "" && response.StopReason != FinishReasonStop || response.IsRefusal() || response.HasToolCalls() {
		return fmt.Errorf("%w: response is not completed structured output", ErrInvalidResponse)
	}
	value := json.RawMessage(response.Text())
	if !jsonObject(value) {
		return fmt.Errorf("%w: structured output must be a JSON object", ErrInvalidResponse)
	}
	if err := validate(schema, value); err != nil {
		return fmt.Errorf("%w: output does not match schema: %v", ErrInvalidResponse, err)
	}
	return nil
}

func DecodeStructuredOutput[T any](response GenerationResponse, schema json.RawMessage, validate SchemaValidator) (T, error) {
	var value T
	if err := ValidateStructuredOutput(response, schema, validate); err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(response.Text()), &value); err != nil {
		return value, fmt.Errorf("%w: cannot decode structured output: %v", ErrInvalidResponse, err)
	}
	return value, nil
}
