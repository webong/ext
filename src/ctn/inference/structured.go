package inference

import (
	"encoding/json"
	"fmt"

	"github.com/webong/ext/pkg/ext-go/engine/validate/jsonschema"
	domain "github.com/webong/ext/res/int/inference"
)

// ValidateStructuredOutput validates completed model output with the Ext
// engine's bounded, offline JSON Schema implementation.
func ValidateStructuredOutput(response domain.GenerationResponse, schema json.RawMessage) error {
	compiled, err := jsonschema.Compile(schema)
	if err != nil {
		return fmt.Errorf("%w: invalid output schema: %v", domain.ErrInvalidRequest, err)
	}
	return domain.ValidateStructuredOutput(response, schema, func(_ json.RawMessage, value json.RawMessage) error {
		return compiled.Validate(value)
	})
}

func DecodeStructuredOutput[T any](response domain.GenerationResponse, schema json.RawMessage) (T, error) {
	var value T
	if err := ValidateStructuredOutput(response, schema); err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(response.Text()), &value); err != nil {
		return value, fmt.Errorf("%w: cannot decode structured output: %v", domain.ErrInvalidResponse, err)
	}
	return value, nil
}
