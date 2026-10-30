// Package inference embedding types define embedding requests and responses.
package inference

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// EmbeddingRequest requests embeddings for one or more inputs.
type EmbeddingRequest struct {
	// Model is the selected embedding model.
	Model string `json:"model"`
	// Inputs are the texts to embed. At least one is required.
	Inputs []string `json:"inputs"`
	// Options are provider-owned options.
	Options json.RawMessage `json:"options,omitempty"`
	// Limits are explicit bounds for this request.
	Limits Limits `json:"limits,omitempty"`
}

// Validate checks the request.
func (r EmbeddingRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if len(r.Inputs) == 0 || len(r.Inputs) > 4096 {
		return fmt.Errorf("%w: inputs must be between 1 and 4096", ErrInvalidRequest)
	}
	total := 0
	for _, input := range r.Inputs {
		total += len(input)
		if total > 32<<20 {
			return fmt.Errorf("%w: inputs exceed 32 MiB", ErrInvalidRequest)
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

// Embedding is one embedding vector.
type Embedding struct {
	// Index is the input position.
	Index int `json:"index"`
	// Vector is the embedding values.
	Vector []float64 `json:"vector"`
}

// EmbeddingResponse is the result of an embedding call.
type EmbeddingResponse struct {
	// Model is the model that served the request.
	Model string `json:"model"`
	// Embeddings are the per-input vectors, in input order.
	Embeddings []Embedding `json:"embeddings"`
	// Usage is token accounting.
	Usage TokenUsage `json:"usage"`
}

// Validate checks the response.
func (r EmbeddingResponse) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("%w: response needs a model", ErrInvalidResponse)
	}
	if len(r.Embeddings) == 0 {
		return fmt.Errorf("%w: response needs at least one embedding", ErrInvalidResponse)
	}
	seen := map[int]bool{}
	for _, e := range r.Embeddings {
		if seen[e.Index] {
			return fmt.Errorf("%w: duplicate embedding index %d", ErrInvalidResponse, e.Index)
		}
		seen[e.Index] = true
		if len(e.Vector) == 0 {
			return fmt.Errorf("%w: embedding %d has an empty vector", ErrInvalidResponse, e.Index)
		}
	}
	if r.Usage.Input < 0 || r.Usage.Total < 0 {
		return fmt.Errorf("%w: usage must not be negative", ErrInvalidResponse)
	}
	return nil
}

var _ = errors.Unwrap
