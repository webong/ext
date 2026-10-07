// Package plugintest supplies a reusable conformance suite for CTX backends.
// Factories bind Guest() locally or in a dedicated subprocess and must return
// a fresh owned backend each time. The suite performs real transport calls.
package plugintest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
)

func Descriptor() plugin.Descriptor {
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "ctx/conformance", Revision: "fixture-1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "ext.conformance", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}, {Name: "wait"}, {Name: "private-error"}, {Name: "public-error"}}}}}
}
func Guest() (*plugin.Guest, error) {
	return plugin.NewGuest(Descriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		switch r.Operation {
		case "wait":
			<-ctx.Done()
			return nil, ctx.Err()
		case "private-error":
			return nil, errors.New("secret diagnostic")
		case "public-error":
			return nil, &plugin.RemoteError{Code: "busy", Message: "try later", RetryAfterMilliseconds: 10}
		default:
			return r.Payload, nil
		}
	}})
}

type Factory func(context.Context) (plugin.Backend, error)

// Host is the common session surface exercised by backend conformance. A
// language binding may run the same suite without wrapping a Go Session.
type Host interface {
	Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)
	State() plugin.State
	Close(context.Context) error
	Abort() error
}
type OpenHost func(context.Context, plugin.Descriptor, plugin.Options) (Host, error)

func Run(t *testing.T, factory Factory) {
	RunWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Host, error) {
		return plugin.Open(ctx, d, o)
	})
}
func RunWithHost(t *testing.T, factory Factory, openHost OpenHost) {
	t.Helper()
	open := func(t *testing.T, d plugin.Descriptor, authorize func(context.Context, plugin.Request) error) Host {
		t.Helper()
		s, err := openHost(context.Background(), d, plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: authorize, Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) { return factory(ctx) }, Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Abort() })
		return s
	}
	allow := func(context.Context, plugin.Request) error { return nil }
	ref := Descriptor().Contracts[0].ContractRef
	t.Run("roundtrip-concurrent-errors-drain", func(t *testing.T) {
		s := open(t, Descriptor(), allow)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, err := s.Call(context.Background(), ref, "echo", json.RawMessage(`{"value":7}`))
				if err != nil || string(v) != `{"value":7}` {
					t.Errorf("roundtrip %s: %v", v, err)
				}
			}()
		}
		wg.Wait()
		_, err := s.Call(context.Background(), ref, "public-error", nil)
		var remote *plugin.RemoteError
		if !errors.As(err, &remote) || remote.Code != "busy" || remote.RetryAfterMilliseconds != 10 {
			t.Fatalf("public error: %v", err)
		}
		_, err = s.Call(context.Background(), ref, "private-error", nil)
		if !errors.As(err, &remote) || remote.Code != "operation_failed" || remote.Message != "plugin operation failed" {
			t.Fatalf("private error: %v", err)
		}
		if s.State() != plugin.StateReady {
			t.Fatal("domain error destroyed session")
		}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Call(context.Background(), ref, "echo", nil); !errors.Is(err, plugin.ErrClosed) {
			t.Fatal(err)
		}
	})
	t.Run("mismatch", func(t *testing.T) {
		d := Descriptor()
		d.Identity.Revision = "other"
		_, err := openHost(context.Background(), d, plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: allow, Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) { return factory(ctx) }})
		if !errors.Is(err, plugin.ErrMismatch) {
			t.Fatal(err)
		}
	})
	t.Run("authorization", func(t *testing.T) {
		s := open(t, Descriptor(), func(context.Context, plugin.Request) error { return plugin.ErrDenied })
		if _, err := s.Call(context.Background(), ref, "echo", nil); !errors.Is(err, plugin.ErrDenied) {
			t.Fatal(err)
		}
		if s.State() != plugin.StateReady {
			t.Fatal("denial destroyed session")
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		s := open(t, Descriptor(), allow)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		_, err := s.Call(ctx, ref, "wait", nil)
		if err == nil {
			t.Fatal("canceled call succeeded")
		}
		if s.State() != plugin.StateFailed {
			t.Fatal("canceled invocation did not fail session")
		}
	})
	t.Run("close-interrupts-io", func(t *testing.T) {
		b, err := factory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		d, err := b.Handshake(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := b.Invoke(ctx, plugin.Request{APIVersion: plugin.APIVersion, ID: "closing", Plugin: d.Identity, Contract: ref, Operation: "wait", Deadline: time.Now().Add(time.Second)})
			done <- err
		}()
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Close did not interrupt I/O")
		}
	})
}
