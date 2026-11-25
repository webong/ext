package ledger_test

import (
	"context"
	"errors"
	"fmt"
	store "github.com/webong/ext/pkg/ext-go/core/ledger"
	"testing"
)

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
