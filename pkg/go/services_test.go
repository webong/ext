//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/inprocess"
	"github.com/webong/ctx/pkg/plugin/plugintest"
	"github.com/webong/ctx/pkg/plugin/schema"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func service(t *testing.T, name string, input any) (json.RawMessage, error) {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return EngineCall(name, data)
}
func TestEngineServices(t *testing.T) {
	d := plugintest.Descriptor()
	if _, err := service(t, "descriptor.validate", d); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"contract": d.Contracts[0].ContractRef, "operation": "echo"}
	cases := []struct {
		candidates []plugin.Descriptor
		want       error
	}{
		{[]plugin.Descriptor{d}, nil}, {[]plugin.Descriptor{}, plugin.ErrNotFound}, {[]plugin.Descriptor{d, d}, plugin.ErrAmbiguous},
	}
	for _, c := range cases {
		_, err := service(t, "descriptor.select", map[string]any{"candidates": c.candidates, "requirement": want})
		if !errors.Is(err, c.want) {
			t.Fatalf("select: %v != %v", err, c.want)
		}
	}
	changed := d.Clone()
	changed.Identity.Revision = "different"
	if _, err := service(t, "descriptor.match", map[string]any{"selected": d, "actual": changed}); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal(err)
	}
	out, err := service(t, "protocol.negotiate", map[string]any{"preferred": []string{"ctx.plugin/v2", "ctx.plugin/v1"}, "offered": []string{"ctx.plugin/v1"}})
	if err != nil || string(out) != `"ctx.plugin/v1"` {
		t.Fatalf("%s: %v", out, err)
	}
	if _, err := service(t, "protocol.negotiate", map[string]any{"preferred": []string{"v1", "v1"}, "offered": []string{"v1"}}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
}
func TestSchemaDifferential(t *testing.T) {
	data, err := os.ReadFile("../../pkg/plugin/testdata/schema-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Schema         schema.Schema
		Valid, Invalid []json.RawMessage
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if _, err := service(t, "schema.validate", c.Schema); err != nil {
			t.Fatal(err)
		}
		for _, v := range append(c.Valid, c.Invalid...) {
			_, got := service(t, "schema.check", map[string]any{"schema": c.Schema, "value": v})
			want := c.Schema.Check(v)
			if (got == nil) != (want == nil) {
				t.Fatalf("schema %#v, value %s: C %v, Go %v", c.Schema, v, got, want)
			}
		}
	}
	for _, s := range []schema.Schema{{Type: "alien"}, {Type: "array"}, {Type: "string", MinLength: -1}, {Type: "string", Enum: []string{"x", "x"}}, {Type: "object", Required: []string{"missing"}}} {
		_, got := service(t, "schema.validate", s)
		if (got == nil) != (s.Validate() == nil) {
			t.Fatalf("C %v, Go %v", got, s.Validate())
		}
	}
}
func TestExtensionConformance(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		name := "serialized"
		if concurrent {
			name = "concurrent"
		}
		t.Run(name, func(t *testing.T) {
			plugintest.Run(t, func(context.Context) (plugin.Backend, error) {
				h, err := NewWithBackend(plugintest.Descriptor(), BackendOptions{Concurrent: concurrent, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
					g, e := plugintest.Guest()
					if e != nil {
						return nil, e
					}
					return inprocess.New(g)
				}}, allow, allow)
				if err == nil {
					t.Cleanup(h.Destroy)
				}
				return h, err
			})
		})
	}
}
func TestExtensionDrainAndConcurrency(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	g, err := plugin.NewGuest(plugintest.Descriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			return r.Payload, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewWithBackend(plugintest.Descriptor(), BackendOptions{Concurrent: true, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return inprocess.New(g) }}, allow, allow)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Destroy()
	if err = h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, e := h.CallRaw(context.Background(), request("echo")); done <- e }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("extension dispatch is serialized")
		}
	}
	if err = h.Drain(5 * time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if h.State() != plugin.StateReady {
		t.Fatal(h.State())
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 2 {
		t.Fatal(maximum.Load())
	}
	if err = h.Drain(time.Second); err != nil {
		t.Fatal(err)
	}
	if h.State() != plugin.StateClosed {
		t.Fatal(h.State())
	}
}
func TestExtensionVerifyBeforeConnect(t *testing.T) {
	var connected bool
	h, err := NewWithBackend(plugintest.Descriptor(), BackendOptions{Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
		connected = true
		return nil, plugin.ErrInvalid
	}}, func([]byte) error { return plugin.ErrDenied }, allow)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Destroy()
	if err = h.Start(context.Background()); !errors.Is(err, plugin.ErrDenied) || connected {
		t.Fatalf("%v, connected=%v", err, connected)
	}
}

func TestQueuedCancellationAndDrainContext(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	g, _ := plugin.NewGuest(plugintest.Descriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return r.Payload, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	h, err := NewWithBackend(plugintest.Descriptor(), BackendOptions{Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return inprocess.New(g) }}, allow, allow)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Destroy()
	if err = h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := h.Call(context.Background(), plugintest.Descriptor().Contracts[0].ContractRef, "echo", json.RawMessage(`7`))
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no dispatch")
	}
	canceled, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	_, err = h.CallRaw(canceled, request("echo"))
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if h.State() != plugin.StateReady {
		t.Fatal("queued cancellation failed session", h.State())
	}
	draining, stop := context.WithCancel(context.Background())
	drain := make(chan error, 1)
	go func() { drain <- h.DrainContext(draining) }()
	deadline := time.Now().Add(time.Second)
	for h.State() != plugin.StateDraining && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.State() != plugin.StateDraining {
		t.Fatal("drain did not start")
	}
	if err = h.Drain(time.Second); !errors.Is(err, plugin.ErrDraining) {
		t.Fatal(err)
	}
	stop()
	select {
	case err = <-drain:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("drain cancellation stalled")
	}
	if h.State() != plugin.StateReady {
		t.Fatal("drain cancellation did not reopen", h.State())
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = h.DrainContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestBackendContextAndGeneratedCall(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "value")
	var ids []string
	g, _ := plugin.NewGuest(plugintest.Descriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		if ctx.Value(key{}) != "value" {
			return nil, errors.New("lost invocation context")
		}
		ids = append(ids, r.ID)
		return r.Payload, nil
	}})
	h, err := NewWithBackend(plugintest.Descriptor(), BackendOptions{Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		if ctx.Value(key{}) != "value" {
			return nil, errors.New("lost startup context")
		}
		return inprocess.New(g)
	}}, allow, allow)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Destroy()
	if err = h.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err := h.Call(ctx, plugintest.Descriptor().Contracts[0].ContractRef, "echo", json.RawMessage(`{"ok":true}`))
		if err != nil || string(out) != `{"ok":true}` {
			t.Fatalf("%s %v", out, err)
		}
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatal(ids)
	}
}

func TestDrainIncludesQueuedCalls(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	g, _ := plugin.NewGuest(plugintest.Descriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return r.Payload, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	h, err := NewWithBackend(plugintest.Descriptor(), BackendOptions{Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return inprocess.New(g) }}, allow, allow)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Destroy()
	if err = h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { _, e := h.CallRaw(context.Background(), request("echo")); results <- e }()
	<-entered
	go func() { _, e := h.CallRaw(context.Background(), request("echo")); results <- e }()
	// The first backend call remains blocked, allowing the second to queue.
	time.Sleep(20 * time.Millisecond)
	drain := make(chan error, 1)
	go func() { drain <- h.DrainContext(context.Background()) }()
	until := time.Now().Add(time.Second)
	for h.State() != plugin.StateDraining && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err = <-drain; err != nil {
		t.Fatal(err)
	}
}
