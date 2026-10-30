package inference

import (
	"context"
	"errors"
	"math"
	"testing"
)

type batchEmbedder struct{}

func (batchEmbedder) Embed(_ context.Context, request EmbeddingRequest) (EmbeddingResponse, error) {
	return EmbeddingResponse{Model: request.Model, Embeddings: []Embedding{{Index: 0, Vector: []float64{float64(len(request.Inputs[0])), 1}}}}, nil
}

func TestEmbedBatchPreservesOrder(t *testing.T) {
	requests := []EmbeddingRequest{{Model: "m", Inputs: []string{"a"}}, {Model: "m", Inputs: []string{"abc"}}}
	results, err := EmbedBatch(context.Background(), batchEmbedder{}, requests, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Embeddings[0].Vector[0] != 1 || results[1].Embeddings[0].Vector[0] != 3 {
		t.Fatalf("results were reordered: %+v", results)
	}
}

func TestVectorHelpers(t *testing.T) {
	value, err := CosineSimilarity([]float64{1e300, 0}, []float64{1e300, 0})
	if err != nil || value != 1 {
		t.Fatalf("large vectors: %v, %v", value, err)
	}
	if _, err := NormalizeVector([]float64{math.NaN()}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nonfinite vector accepted: %v", err)
	}
	if _, err := CosineSimilarity([]float64{1}, []float64{1, 2}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mismatched dimensions accepted: %v", err)
	}
}
