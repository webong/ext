//go:build ext_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/instance"
)

func TestCInstanceReplacementLeasesAndDrain(t *testing.T) {
	var disposed atomic.Int32
	m, err := NewInstances(instance.Options[string]{Capacity: 3, Validate: func(json.RawMessage) error { return nil }, Create: func(_ context.Context, _ string, config json.RawMessage) (string, func() error, error) {
		return string(config), func() error { disposed.Add(1); return nil }, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := m.Configure(ctx, "tenant/one", "r1", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	first, _ := m.Acquire("tenant/one")
	if err := m.Configure(ctx, "tenant/one", "r1", json.RawMessage(`{"v":2}`)); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal("reused revision", err)
	}
	if err := m.Configure(ctx, "tenant/one", "r2", json.RawMessage(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	second, _ := m.Acquire("tenant/one")
	if disposed.Load() != 0 || first.Value == second.Value {
		t.Fatal("replacement destroyed lease")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	_ = first.Release()
	if disposed.Load() != 1 {
		t.Fatal(disposed.Load())
	}
	short, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if err := m.Close(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := m.Acquire("tenant/one"); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
	_ = second.Release()
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if disposed.Load() != 2 {
		t.Fatal(disposed.Load())
	}
}

func TestCInstanceFailedReplacementAndConcurrentClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var disposed atomic.Int32
	m, err := NewInstances(instance.Options[string]{Capacity: 4, Validate: func(json.RawMessage) error { return nil }, Create: func(ctx context.Context, key string, raw json.RawMessage) (string, func() error, error) {
		if string(raw) == `"slow"` {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return "", nil, ctx.Err()
			}
		}
		if string(raw) == `"bad"` {
			return "", nil, errors.New("factory failed")
		}
		return string(raw), func() error { disposed.Add(1); return nil }, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := m.Configure(ctx, "one", "r1", json.RawMessage(`"ok"`)); err != nil {
		t.Fatal(err)
	}
	if err := m.Configure(ctx, "one", "r2", json.RawMessage(`"bad"`)); err == nil {
		t.Fatal("failed factory accepted")
	}
	lease, err := m.Acquire("one")
	if err != nil || lease.Value != `"ok"` {
		t.Fatal(lease, err)
	}
	_ = lease.Release()
	done := make(chan error, 1)
	go func() { done <- m.Configure(ctx, "one", "r3", json.RawMessage(`"slow"`)) }()
	<-entered
	short, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if err := m.Close(short); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, plugin.ErrClosed) && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if disposed.Load() != 1 {
		t.Fatal("factory result leaked", disposed.Load())
	}
}
