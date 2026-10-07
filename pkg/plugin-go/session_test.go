//go:build ext_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ext/pkg/plugin"
	"sync/atomic"
	"testing"
	"time"
)

type testBackend struct {
	d      plugin.Descriptor
	invoke func(context.Context, plugin.Request) (plugin.Response, error)
	calls  atomic.Int32
	closes atomic.Int32
}

func (b *testBackend) Handshake(context.Context) (plugin.Descriptor, error) { return b.d, nil }
func (b *testBackend) Invoke(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	b.calls.Add(1)
	if b.invoke != nil {
		return b.invoke(ctx, r)
	}
	return plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
}
func (b *testBackend) Close() error { b.closes.Add(1); return nil }
func referenceOptions(b *testBackend) plugin.Options {
	return plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }, Authorize: func(context.Context, plugin.Request) error { return nil }}
}

// Deliberately delays cancellation notification, making the timer scheduling
// race deterministic without depending on which real context timer fires first.
type delayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c delayedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestCSessionLateResponseFailsSessionBeforeContextTimerNotification(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "payload", true: "remote-error"}[remote], func(t *testing.T) {
			d := testDescriptor()
			b := &testBackend{d: d, invoke: func(ctx context.Context, r plugin.Request) (plugin.Response, error) {
				time.Sleep(time.Until(r.Deadline) + time.Millisecond)
				// The C binding installs its own deadline timer.
				response := plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Payload: json.RawMessage("null")}
				if remote {
					response.Payload = nil
					response.Error = &plugin.RemoteError{Code: "operation_failed", Message: "plugin operation failed"}
				}
				return response, nil
			}}
			s, err := Open(context.Background(), d, referenceOptions(b))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Abort()
			ctx := delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(5 * time.Millisecond)}
			_, err = s.Call(ctx, d.Contracts[0].ContractRef, "inspect", nil)
			if !errors.Is(err, context.DeadlineExceeded) || s.State() != plugin.StateFailed || b.closes.Load() != 1 {
				t.Fatalf("late result: error=%v state=%s closes=%d", err, s.State(), b.closes.Load())
			}
		})
	}
}

func TestCSessionAdmissionAndPinnedCopies(t *testing.T) {
	d := testDescriptor()
	b := &testBackend{d: d.Clone()}
	opts := referenceOptions(b)
	connected := false
	opts.Connect = func(context.Context, plugin.Descriptor) (plugin.Backend, error) { connected = true; return b, nil }
	opts.Verify = func(context.Context, plugin.Descriptor) error { return errors.New("untrusted") }
	if _, err := Open(context.Background(), d, opts); !errors.Is(err, plugin.ErrDenied) || connected {
		t.Fatalf("connect preceded trust: %v", err)
	}
	opts.Verify = nil
	if _, err := Open(context.Background(), d, opts); !errors.Is(err, plugin.ErrDenied) || connected {
		t.Fatalf("nil policy admitted: %v", err)
	}
	opts = referenceOptions(b)
	opts.Verify = func(_ context.Context, copy plugin.Descriptor) error {
		copy.Contracts[0].Operations[0].Name = "tampered"
		return nil
	}
	opts.Authorize = func(_ context.Context, r plugin.Request) error { r.Payload[0] = '['; return nil }
	b.invoke = func(_ context.Context, r plugin.Request) (plugin.Response, error) {
		if string(r.Payload) != "{}" {
			t.Errorf("policy mutated payload: %s", r.Payload)
		}
		return plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
	}
	s, err := Open(context.Background(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	d.Contracts[0].Operations[0].Name = "mutated-after-open"
	ref := plugin.ContractRef{Name: "example.work", Version: "v1"}
	if _, err := s.Call(context.Background(), ref, "inspect", json.RawMessage("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(context.Background(), ref, "remove", nil); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatal(err)
	}
	if b.calls.Load() != 1 {
		t.Fatal("unsupported call dispatched")
	}
	bad := testDescriptor()
	bad.Identity.Revision = "other"
	b2 := &testBackend{d: bad}
	if _, err := Open(context.Background(), testDescriptor(), referenceOptions(b2)); !errors.Is(err, plugin.ErrMismatch) || b2.closes.Load() != 1 {
		t.Fatalf("mismatch did not close backend: %v", err)
	}
}

func TestCSessionDeniedCallNeverDispatched(t *testing.T) {
	d := testDescriptor()
	b := &testBackend{d: d}
	opts := referenceOptions(b)
	opts.Authorize = func(context.Context, plugin.Request) error { return errors.New("no grant") }
	s, err := Open(context.Background(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "apply", nil); !errors.Is(err, plugin.ErrDenied) {
		t.Fatal(err)
	}
	if b.calls.Load() != 0 {
		t.Fatal("denied call reached backend")
	}
}

func TestCSessionDrainTimeoutRestoresAdmission(t *testing.T) {
	d := testDescriptor()
	entered := make(chan struct{})
	release := make(chan struct{})
	b := &testBackend{d: d, invoke: func(ctx context.Context, r plugin.Request) (plugin.Response, error) {
		if r.ID == "1" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return plugin.Response{}, ctx.Err()
			}
		}
		return plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
	}}
	s, err := Open(context.Background(), d, referenceOptions(b))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	callDone := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil)
		callDone <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if s.State() != plugin.StateReady || b.closes.Load() != 0 {
		t.Fatal("drain timeout closed backend")
	}
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-callDone; err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.State() != plugin.StateClosed || b.closes.Load() != 1 {
		t.Fatal("session did not close once")
	}
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
}

func TestCSessionResponseMismatchFailsSession(t *testing.T) {
	d := testDescriptor()
	b := &testBackend{d: d, invoke: func(context.Context, plugin.Request) (plugin.Response, error) {
		return plugin.Response{APIVersion: plugin.APIVersion, ID: "wrong", Payload: json.RawMessage("null")}, nil
	}}
	s, err := Open(context.Background(), d, referenceOptions(b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal(err)
	}
	if s.State() != plugin.StateFailed || b.closes.Load() != 1 {
		t.Fatal("bad response left session usable")
	}
}

func TestCSessionCloseDrainsAndRejectsNewCalls(t *testing.T) {
	d := testDescriptor()
	entered, release := make(chan struct{}), make(chan struct{})
	b := &testBackend{d: d, invoke: func(ctx context.Context, r plugin.Request) (plugin.Response, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return plugin.Response{}, ctx.Err()
		}
		return plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
	}}
	s, err := Open(context.Background(), d, referenceOptions(b))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	callDone := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil)
		callDone <- err
	}()
	<-entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close(context.Background()) }()
	until := time.Now().Add(time.Second)
	for s.State() != plugin.StateDraining {
		if time.Now().After(until) {
			t.Fatal("close did not begin draining")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil); !errors.Is(err, plugin.ErrDraining) {
		t.Fatal(err)
	}
	if b.closes.Load() != 0 {
		t.Fatal("backend closed before admitted invocation finished")
	}
	close(release)
	if err := <-callDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if b.closes.Load() != 1 {
		t.Fatal("backend was not closed once")
	}
}

func testDescriptor() plugin.Descriptor {
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/worker", Revision: "r1", Version: "1.0.0"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.work", Version: "v1"}, Operations: []plugin.Operation{{Name: "inspect", Surface: "observation"}, {Name: "apply", Surface: "action"}}}}}
}
