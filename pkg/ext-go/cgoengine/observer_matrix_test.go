//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"context"
	"testing"
	"time"
)

// TestEventCodeFollowsTheContext pins what the engine's cancel signal and its deadline cannot
// tell apart: when the call's context is done, the event says why the context ended. On Windows
// the engine often sees the Go deadline's cancel signal before its own deadline (a silent child
// at the handshake reported "canceled" where the Go session says "deadline_exceeded").
func TestEventCodeFollowsTheContext(t *testing.T) {
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		code string
		want string
	}{
		{"timeout seen as a cancellation", expired, "canceled", "deadline_exceeded"},
		{"timeout seen as a timeout", expired, "deadline_exceeded", "deadline_exceeded"},
		{"cancellation seen as a timeout", canceled, "deadline_exceeded", "canceled"},
		{"cancellation seen as a cancellation", canceled, "canceled", "canceled"},
		{"engine's own deadline, context still live", context.Background(), "deadline_exceeded", "deadline_exceeded"},
		{"engine's cancel, context still live", context.Background(), "canceled", "canceled"},
		{"other codes are the engine's", expired, "mismatch", "mismatch"},
		{"success", expired, "ok", "ok"},
	} {
		if got := settledEventCode(c.ctx, c.code); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
