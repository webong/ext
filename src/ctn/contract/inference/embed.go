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
	// Inputs are the texts to embed. A request has Inputs or TokenInputs, not both.
	Inputs []string `json:"inputs,omitempty"`
	// TokenInputs are inputs already tokenized for the model, one token-ID array per input,
	// for providers that accept them (OpenAI does).
	TokenInputs [][]int64 `json:"tokenInputs,omitempty"`
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
	if (len(r.Inputs) == 0) == (len(r.TokenInputs) == 0) {
		return fmt.Errorf("%w: exactly one of inputs or tokenInputs is required", ErrInvalidRequest)
	}
	if r.InputCount() > 4096 {
		return fmt.Errorf("%w: inputs must be between 1 and 4096", ErrInvalidRequest)
	}
	total := 0
	for _, input := range r.Inputs {
		total += len(input)
		if total > 32<<20 {
			return fmt.Errorf("%w: inputs exceed 32 MiB", ErrInvalidRequest)
		}
	}
	tokens := 0
	for _, input := range r.TokenInputs {
		if len(input) == 0 {
			return fmt.Errorf("%w: a token input must not be empty", ErrInvalidRequest)
		}
		tokens += len(input)
		if tokens > 4<<20 {
			return fmt.Errorf("%w: token inputs exceed 4 Mi tokens", ErrInvalidRequest)
		}
		for _, token := range input {
			if token < 0 {
				return fmt.Errorf("%w: token IDs must not be negative", ErrInvalidRequest)
			}
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

// InputCount is the number of inputs, texts or token arrays, the request embeds.
func (r EmbeddingRequest) InputCount() int {
	return len(r.Inputs) + len(r.TokenInputs)
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
