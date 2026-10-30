package inference

import (
	"context"
	"fmt"
	"math"
	"sync"
)

// EmbedBatch performs exactly the caller-supplied requests in input order.
// Failure cancels outstanding work; it never retries or selects another model.
func EmbedBatch(ctx context.Context, provider Embedder, requests []EmbeddingRequest, concurrency int) ([]EmbeddingResponse, error) {
	if ctx == nil || provider == nil || concurrency < 1 || concurrency > 32 || len(requests) > 1024 {
		return nil, fmt.Errorf("%w: batch requires a provider, at most 1024 requests, and concurrency 1-32", ErrInvalidRequest)
	}
	for _, request := range requests {
		if err := request.Validate(); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]EmbeddingResponse, len(requests))
	var mu sync.Mutex
	var firstErr error
	next := 0
	var workers sync.WaitGroup
	for i := 0; i < min(concurrency, len(requests)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				mu.Lock()
				index := next
				next++
				mu.Unlock()
				if index >= len(requests) || ctx.Err() != nil {
					return
				}
				response, err := provider.Embed(ctx, requests[index])
				if err == nil {
					err = validateBatchEmbedding(response, requests[index])
				}
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("embedding request %d: %w", index, err)
					}
					mu.Unlock()
					cancel()
					return
				}
				results[index] = response
			}
		}()
	}
	workers.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func validateBatchEmbedding(response EmbeddingResponse, request EmbeddingRequest) error {
	if err := response.Validate(); err != nil {
		return err
	}
	if len(response.Embeddings) != len(request.Inputs) {
		return fmt.Errorf("%w: embedding count does not match inputs", ErrInvalidResponse)
	}
	dimensions := 0
	for i, embedding := range response.Embeddings {
		if embedding.Index != i || len(embedding.Vector) > 65536 {
			return fmt.Errorf("%w: embedding index or dimension is invalid", ErrInvalidResponse)
		}
		if dimensions == 0 {
			dimensions = len(embedding.Vector)
		}
		if len(embedding.Vector) != dimensions {
			return fmt.Errorf("%w: embedding dimensions differ", ErrInvalidResponse)
		}
		for _, value := range embedding.Vector {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("%w: embedding contains a nonfinite value", ErrInvalidResponse)
			}
		}
	}
	return nil
}

// NormalizeVector returns a new unit vector without mutating the input.
func NormalizeVector(vector []float64) ([]float64, error) {
	if len(vector) == 0 || len(vector) > 65536 {
		return nil, fmt.Errorf("%w: vector size is invalid", ErrInvalidRequest)
	}
	scale := 0.0
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%w: vector contains a nonfinite value", ErrInvalidRequest)
		}
		scale = math.Max(scale, math.Abs(value))
	}
	if scale == 0 {
		return nil, fmt.Errorf("%w: zero vector cannot be normalized", ErrInvalidRequest)
	}
	sum := 0.0
	for _, value := range vector {
		n := value / scale
		sum += n * n
	}
	norm := math.Sqrt(sum)
	out := make([]float64, len(vector))
	for i, value := range vector {
		out[i] = (value / scale) / norm
	}
	return out, nil
}

// CosineSimilarity compares two finite vectors with equal dimensions.
func CosineSimilarity(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: vector dimensions differ", ErrInvalidRequest)
	}
	x, err := NormalizeVector(a)
	if err != nil {
		return 0, err
	}
	y, err := NormalizeVector(b)
	if err != nil {
		return 0, err
	}
	dot := 0.0
	for i := range x {
		dot += x[i] * y[i]
	}
	return math.Max(-1, math.Min(1, dot)), nil
}
