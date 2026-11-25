package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	native "github.com/webong/ext/pkg/ext-go/cengine/plugin"
	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest/enginetest"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/inprocess"
)

func TestNativeSession(t *testing.T) {
	path := os.Getenv("EXT_PLUGIN_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_PLUGIN_LIBRARY")
	}
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	host, err := native.Bind(lib)
	if err != nil {
		t.Fatal(err)
	}
	e := enginetest.Engine{Open: func(ctx context.Context, d engine.Descriptor, o engine.Options) (enginetest.Session, error) {
		return host.Open(ctx, d, o)
	}}
	for _, test := range []struct {
		name string
		run  func(*testing.T, enginetest.Engine)
	}{
		{"SessionErrors", enginetest.SessionErrorCausesMatchGo},
		{"OpenErrors", enginetest.OpenErrorCausesMatchGo},
		{"DrainErrors", enginetest.DrainErrorCausesMatchGo},
		{"SessionEvents", enginetest.SessionObserverEventsMatchGo},
		{"OpenEvents", enginetest.OpenObserverEventsMatchGo},
		{"DrainEvents", enginetest.DrainObserverEventsMatchGo},
	} {
		t.Run(test.name, func(t *testing.T) { test.run(t, e) })
	}
}

func TestNativeRegistryReleasesClosedTargets(t *testing.T) {
	path := os.Getenv("EXT_PLUGIN_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_PLUGIN_LIBRARY")
	}
	n, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := native.Bind(n)
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ref := engine.ContractRef{Name: "fixture.echo", Version: "v1"}
	d := engine.Descriptor{APIVersion: engine.APIVersion, Identity: engine.Identity{ID: "fixture", Revision: "1"}, Contracts: []engine.Contract{{ContractRef: ref, Operations: []engine.Operation{{Name: "echo"}}}}}
	open := func() *native.Session {
		g, err := engine.NewGuest(d, engine.GuestOptions{Handler: func(_ context.Context, r engine.Request) (json.RawMessage, error) { return r.Payload, nil }})
		if err != nil {
			t.Fatal(err)
		}
		s, err := l.Open(context.Background(), d, engine.Options{Verify: func(context.Context, engine.Descriptor) error { return nil }, Authorize: func(context.Context, engine.Request) error { return nil }, Connect: func(context.Context, engine.Descriptor) (engine.Backend, error) { return inprocess.New(g) }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Abort() })
		return s
	}
	first, second := open(), open()
	if err := r.Register(ref, first); err != nil {
		t.Fatal(err)
	}
	if err := first.Abort(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		err = r.Register(ref, second)
		if err == nil {
			break
		}
		if !errors.Is(err, native.ErrRegistryExists) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if err := r.Unregister(ref); err != nil {
		t.Fatal(err)
	}
}

type contextKey struct{}
type contextBackend struct{ descriptor engine.Descriptor }

func (b contextBackend) Handshake(context.Context) (engine.Descriptor, error) {
	return b.descriptor, nil
}
func (b contextBackend) Close() error { return nil }
func (b contextBackend) Invoke(ctx context.Context, r engine.Request) (engine.Response, error) {
	if ctx.Value(contextKey{}) != "caller" {
		return engine.Response{}, errors.New("backend lost caller context")
	}
	return engine.Response{}, io.EOF
}

func TestNativeSessionPreservesCallerContextAndTransportError(t *testing.T) {
	if os.Getenv("EXT_PLUGIN_LIBRARY") == "" {
		t.Skip("requires EXT_PLUGIN_LIBRARY")
	}
	l, err := native.Default()
	if err != nil {
		t.Fatal(err)
	}
	ref := engine.ContractRef{Name: "fixture.echo", Version: "v1"}
	d := engine.Descriptor{APIVersion: engine.APIVersion, Identity: engine.Identity{ID: "fixture", Revision: "1"}, Contracts: []engine.Contract{{ContractRef: ref, Operations: []engine.Operation{{Name: "echo"}}}}}
	var authorized, observed atomic.Bool
	s, err := l.Open(context.Background(), d, engine.Options{
		Verify:  func(context.Context, engine.Descriptor) error { return nil },
		Connect: func(context.Context, engine.Descriptor) (engine.Backend, error) { return contextBackend{d}, nil },
		Authorize: func(ctx context.Context, _ engine.Request) error {
			authorized.Store(ctx.Value(contextKey{}) == "caller")
			return nil
		},
		Observer: func(ctx context.Context, e engine.Event) {
			if e.Stage == "invoke" {
				observed.Store(ctx.Value(contextKey{}) == "caller")
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	_, err = s.Call(context.WithValue(context.Background(), contextKey{}, "caller"), ref, "echo", nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("transport error lost: %v", err)
	}
	if !authorized.Load() || !observed.Load() {
		t.Fatalf("caller context lost: authorized=%v observed=%v", authorized.Load(), observed.Load())
	}
}
