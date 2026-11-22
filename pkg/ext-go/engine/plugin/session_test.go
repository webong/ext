package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type testBackend struct {
	d      Descriptor
	invoke func(context.Context, Request) (Response, error)
	calls  atomic.Int32
	closes atomic.Int32
}

func (b *testBackend) Handshake(context.Context) (Descriptor, error) { return b.d, nil }
func (b *testBackend) Invoke(ctx context.Context, r Request) (Response, error) {
	b.calls.Add(1)
	if b.invoke != nil {
		return b.invoke(ctx, r)
	}
	return Response{APIVersion: APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
}
func (b *testBackend) Close() error { b.closes.Add(1); return nil }
func testOptions(b *testBackend) Options {
	return Options{Verify: func(context.Context, Descriptor) error { return nil }, Connect: func(context.Context, Descriptor) (Backend, error) { return b, nil }, Authorize: func(context.Context, Request) error { return nil }}
}

// Deliberately delays cancellation notification, making the timer scheduling
// race deterministic without depending on which real context timer fires first.
type delayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c delayedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestLateResponseFailsSessionBeforeContextTimerNotification(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "payload", true: "remote-error"}[remote], func(t *testing.T) {
			d := testDescriptor()
			b := &testBackend{d: d, invoke: func(ctx context.Context, r Request) (Response, error) {
				time.Sleep(time.Until(r.Deadline) + time.Millisecond)
				if ctx.Err() != nil {
					t.Fatal("test context must delay its timer notification")
				}
				response := Response{APIVersion: APIVersion, ID: r.ID, Payload: json.RawMessage("null")}
				if remote {
					response.Payload = nil
					response.Error = &RemoteError{Code: "operation_failed", Message: "plugin operation failed"}
				}
				return response, nil
			}}
			s, err := Open(context.Background(), d, testOptions(b))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Abort()
			ctx := delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(5 * time.Millisecond)}
			_, err = s.Call(ctx, d.Contracts[0].ContractRef, "inspect", nil)
			if !errors.Is(err, context.DeadlineExceeded) || s.State() != StateFailed || b.closes.Load() != 1 {
				t.Fatalf("late result: error=%v state=%s closes=%d", err, s.State(), b.closes.Load())
			}
		})
	}
}

func TestAdmissionAndPinnedCopies(t *testing.T) {
	d := testDescriptor()
	b := &testBackend{d: d.Clone()}
	opts := testOptions(b)
	connected := false
	opts.Connect = func(context.Context, Descriptor) (Backend, error) { connected = true; return b, nil }
	opts.Verify = func(context.Context, Descriptor) error { return errors.New("untrusted") }
	if _, err := Open(context.Background(), d, opts); !errors.Is(err, ErrDenied) || connected {
		t.Fatalf("connect preceded trust: %v", err)
	}
	opts.Verify = nil
	if _, err := Open(context.Background(), d, opts); !errors.Is(err, ErrDenied) || connected {
		t.Fatalf("nil policy admitted: %v", err)
	}
	opts = testOptions(b)
	opts.Verify = func(_ context.Context, copy Descriptor) error {
		copy.Contracts[0].Operations[0].Name = "tampered"
		return nil
	}
	opts.Authorize = func(_ context.Context, r Request) error { r.Payload[0] = '['; return nil }
	b.invoke = func(_ context.Context, r Request) (Response, error) {
		if string(r.Payload) != "{}" {
			t.Errorf("policy mutated payload: %s", r.Payload)
		}
		return Response{APIVersion: APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
	}
	s, err := Open(context.Background(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	d.Contracts[0].Operations[0].Name = "mutated-after-open"
	ref := ContractRef{Name: "example.work", Version: "v1"}
	if _, err := s.Call(context.Background(), ref, "inspect", json.RawMessage("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(context.Background(), ref, "remove", nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if b.calls.Load() != 1 {
		t.Fatal("unsupported call dispatched")
	}
	bad := testDescriptor()
	bad.Identity.Revision = "other"
	b2 := &testBackend{d: bad}
	if _, err := Open(context.Background(), testDescriptor(), testOptions(b2)); !errors.Is(err, ErrMismatch) || b2.closes.Load() != 1 {
		t.Fatalf("mismatch did not close backend: %v", err)
	}
}

func TestDeniedCallNeverDispatched(t *testing.T) {
	d := testDescriptor()
	b := &testBackend{d: d}
	opts := testOptions(b)
	opts.Authorize = func(context.Context, Request) error { return errors.New("no grant") }
	s, err := Open(context.Background(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "apply", nil); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if b.calls.Load() != 0 {
		t.Fatal("denied call reached backend")
	}
}

func TestDrainTimeoutRestoresAdmission(t *testing.T) {
	d := testDescriptor()
	entered := make(chan struct{})
	release := make(chan struct{})
	b := &testBackend{d: d, invoke: func(ctx context.Context, r Request) (Response, error) {
		if r.ID == "1" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return Response{}, ctx.Err()
			}
		}
		return Response{APIVersion: APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
	}}
	s, err := Open(context.Background(), d, testOptions(b))
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
	if s.State() != StateReady || b.closes.Load() != 0 {
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
	if s.State() != StateClosed || b.closes.Load() != 1 {
		t.Fatal("session did not close once")
	}
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestResponseMismatchFailsSession(t *testing.T) {
	d := testDescriptor()
	b := &testBackend{d: d, invoke: func(context.Context, Request) (Response, error) {
		return Response{APIVersion: APIVersion, ID: "wrong", Payload: json.RawMessage("null")}, nil
	}}
	s, err := Open(context.Background(), d, testOptions(b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil); !errors.Is(err, ErrMismatch) {
		t.Fatal(err)
	}
	if s.State() != StateFailed || b.closes.Load() != 1 {
		t.Fatal("bad response left session usable")
	}
}

func TestCloseDrainsAndRejectsNewCalls(t *testing.T) {
	d := testDescriptor()
	entered, release := make(chan struct{}), make(chan struct{})
	b := &testBackend{d: d, invoke: func(ctx context.Context, r Request) (Response, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
		return Response{APIVersion: APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
	}}
	s, err := Open(context.Background(), d, testOptions(b))
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
	for s.State() != StateDraining {
		if time.Now().After(until) {
			t.Fatal("close did not begin draining")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil); !errors.Is(err, ErrDraining) {
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
