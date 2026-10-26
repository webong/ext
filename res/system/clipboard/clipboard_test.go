package clipboard

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type memoryBackend struct {
	snapshot Snapshot
}

func (b *memoryBackend) Generation(context.Context) (string, error) {
	return b.snapshot.Generation, nil
}
func (b *memoryBackend) Read(context.Context, int64, []string) (Snapshot, error) {
	return b.snapshot, nil
}

func TestWriteOnlyClipboardUsesGenerationWithoutReading(t *testing.T) {
	backend := &memoryBackend{snapshot: Snapshot{Generation: "g1"}}
	clipboard, err := New(backend, Policy{MaxBytes: 1024, AllowedFormats: []string{"text/plain"}, AllowWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	generation, err := clipboard.Generation(context.Background())
	if err != nil || generation != "g1" {
		t.Fatalf("generation: %q %v", generation, err)
	}
	if _, err := clipboard.Read(context.Background()); !errors.Is(err, ErrDenied) {
		t.Fatalf("write-only read: %v", err)
	}
	if _, err := clipboard.Write(context.Background(), WriteRequest{ExpectedGeneration: generation, Items: []Item{{Representations: []Representation{{Format: "text/plain", Data: []byte("two")}}}}}); err != nil {
		t.Fatal(err)
	}
}
func (b *memoryBackend) Write(_ context.Context, request WriteRequest, _ int64, _ []string) (Receipt, error) {
	if request.ExpectedGeneration != b.snapshot.Generation {
		return Receipt{}, ErrChanged
	}
	b.snapshot = Snapshot{Generation: request.ExpectedGeneration + ":next", Items: request.Items}
	return Receipt{Generation: b.snapshot.Generation, Dispatch: "dispatched"}, nil
}

func TestClipboardLifecycle(t *testing.T) {
	backend := &memoryBackend{snapshot: Snapshot{Generation: "g1", Items: []Item{{Representations: []Representation{{Format: "text/plain", Data: []byte("one")}}}}}}
	clipboard, err := New(backend, Policy{MaxBytes: 1024, AllowedFormats: []string{"text/plain"}, AllowRead: true, AllowWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := clipboard.Read(context.Background())
	if err != nil || snapshot.Generation != "g1" {
		t.Fatalf("read: %+v %v", snapshot, err)
	}
	receipt, err := clipboard.Write(context.Background(), WriteRequest{ExpectedGeneration: snapshot.Generation, Items: []Item{{Representations: []Representation{{Format: "text/plain", Data: []byte("two")}}}}})
	if err != nil || receipt.Generation != "g1:next" {
		t.Fatalf("write: %+v %v", receipt, err)
	}
	if _, err := clipboard.Clear(context.Background(), "g1"); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale clear: %v", err)
	}
	if _, err := clipboard.Clear(context.Background(), receipt.Generation); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.snapshot.Items, []Item{}) {
		t.Fatalf("clear retained items: %+v", backend.snapshot.Items)
	}
}

func TestClipboardPolicyAndBounds(t *testing.T) {
	if _, err := New(&memoryBackend{}, Policy{MaxBytes: 1, AllowedFormats: []string{"text/plain", "text/plain"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate formats: %v", err)
	}
	items := []Item{{Representations: []Representation{{Format: "text/plain", Data: []byte("large")}}}}
	if err := ValidateItems(items, 4, []string{"text/plain"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("size: %v", err)
	}
	if err := ValidateItems(items, 10, []string{"text/html"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("format: %v", err)
	}
}
