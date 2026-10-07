package wasm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
)

// (module (func (export "_start") (loop (br 0))))
// A compute loop has no I/O to unblock; cancellation must interrupt execution.
var spinModule = []byte{
	0, 97, 115, 109, 1, 0, 0, 0,
	1, 4, 1, 96, 0, 0,
	3, 2, 1, 0,
	7, 10, 1, 6, '_', 's', 't', 'a', 'r', 't', 0, 0,
	10, 9, 1, 7, 0, 3, 64, 12, 0, 11, 11,
}

func TestCancelComputeAndWaitForCleanup(t *testing.T) {
	for i := 0; i < 10; i++ {
		b, err := Open(context.Background(), spinModule, Options{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_, err = b.Handshake(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("handshake of a nonresponsive guest: %v", err)
		}
		var closers sync.WaitGroup
		for j := 0; j < 4; j++ {
			closers.Add(1)
			go func() { defer closers.Done(); _ = b.Close() }()
		}
		closers.Wait()
		wait, stop := context.WithTimeout(context.Background(), 2*time.Second)
		err = b.Wait(wait)
		stop()
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("canceled compute guest did not finish cleanup")
		}
	}
}

func TestInvalidModulesAndLimits(t *testing.T) {
	for _, opts := range []Options{{MemoryLimitPages: 65537}, {MaxStderrBytes: -1}} {
		if _, err := Open(context.Background(), spinModule, opts); !errors.Is(err, plugin.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := Open(context.Background(), nil, Options{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), spinModule[:8], Options{}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatal(err)
	}
}
