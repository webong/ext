package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/webong/ext/res/graph/store"
	"github.com/webong/ext/res/graph/store/storetest"
)

const testNS = "test.example"

func TestMemoryStoreFixtures(t *testing.T) {
	storetest.Run(t, storetest.Backend{Open: func(_ testing.TB, _ string, o store.Options) store.Store {
		return store.NewMemoryWithOptions(o)
	}})
}

func TestFingerprintFixtures(t *testing.T) { storetest.RunFingerprints(t) }

func TestMemoryStoreBehaviour(t *testing.T) {
	storetest.RunBehaviour(t, storetest.Backend{Open: func(_ testing.TB, _ string, o store.Options) store.Store {
		return store.NewMemoryWithOptions(o)
	}})
}

func TestCodeCoversEverySentinel(t *testing.T) {
	cases := map[error]string{
		store.ErrInvalid: "invalid", store.ErrInvalidName: "invalid", store.ErrLimitRequired: "invalid",
		store.ErrSchemaViolation: "invalid", store.ErrIncidentEdges: "invalid", store.ErrNotFound: "not_found",
		store.ErrNotRegistered: "not_found", store.ErrDanglingEdge: "not_found", store.ErrConflict: "changed",
		store.ErrIdempotency: "exists", store.ErrSchemaMismatch: "exists", store.ErrSecretValue: "denied",
		store.ErrTooLarge: "too_large", store.ErrCursorExpired: "expired", store.ErrUnsupported: "unsupported",
		store.ErrClosed: "unavailable", store.ErrBusy: "busy", context.Canceled: "canceled", context.DeadlineExceeded: "timeout",
		errors.New("disk on fire"): "io", nil: "",
	}
	for err, want := range cases {
		if got := store.Code(fmt.Errorf("wrapped: %w", err)); err != nil && got != want {
			t.Errorf("Code(%v) = %q, want %q", err, got, want)
		}
	}
	if store.Code(nil) != "" {
		t.Fatal("Code(nil) is not empty")
	}
}

func TestCanceledContextFailsFirst(t *testing.T) {
	s := store.NewMemory()
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Register(ctx, store.Schema{Namespace: testNS, Version: "1"}); store.Code(err) != "canceled" {
		t.Fatalf("register: %v", err)
	}
	if _, err := s.Apply(ctx, store.Transaction{Namespace: "bad//name"}); store.Code(err) != "canceled" {
		t.Fatalf("apply: %v", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := s.Snapshot(expired, testNS); store.Code(err) != "timeout" {
		t.Fatalf("snapshot: %v", err)
	}
}
