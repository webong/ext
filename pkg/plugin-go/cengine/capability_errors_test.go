//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/author"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/capability"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
	"github.com/webong/ext/pkg/plugin-go/contract/inprocess"
	"github.com/webong/ext/pkg/plugin-go/contract/schema"
	"testing"
)

// capabilityRig stacks the capability helpers on either the Go building blocks
// (registry, instance manager, session) or the C-backed ones.
func capabilityRig(t *testing.T, c bool, health func(context.Context) (capability.Health, error), scope func(context.Context, plugin.Request, string) (string, error)) (author.Caller, func(key string) string, func()) {
	t.Helper()
	cfg := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"name": {Type: "string"}, "count": {Type: "integer"}}, Required: []string{"name"}}
	opts := instance.Options[string]{Capacity: 2, Validate: func(raw json.RawMessage) error { return cfg.Check(raw) },
		Create: func(_ context.Context, _ string, raw json.RawMessage) (string, func() error, error) {
			return string(raw), func() error { return nil }, nil
		}}
	var closers []func()
	var r *author.Registry
	var configurer capability.Configurer
	var acquire func(string) string
	var err error
	if c {
		r, err = NewRegistry(plugin.Identity{ID: "example/capability", Revision: "r1"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewInstances(opts)
		if err != nil {
			t.Fatal(err)
		}
		closers = append(closers, func() { m.Close(context.Background()) })
		configurer = m
		acquire = func(k string) string {
			l, err := m.Acquire(k)
			if err != nil {
				return "none:" + describe(err)
			}
			defer l.Release()
			return l.Value + "@" + l.Revision
		}
	} else {
		r, err = author.New(plugin.Identity{ID: "example/capability", Revision: "r1"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := instance.New(opts)
		if err != nil {
			t.Fatal(err)
		}
		closers = append(closers, func() { m.Close(context.Background()) })
		configurer = m
		acquire = func(k string) string {
			l, err := m.Acquire(k)
			if err != nil {
				return "none:" + describe(err)
			}
			defer l.Release()
			return l.Value + "@" + l.Revision
		}
	}
	if err := capability.RegisterConfiguration(r, configurer, cfg, scope); err != nil {
		t.Fatal(err)
	}
	if err := capability.RegisterHealth(r, health); err != nil {
		t.Fatal(err)
	}
	authorize := func(context.Context, plugin.Request) error { return nil }
	var backend plugin.Backend
	var open func() (author.Caller, error)
	connect := func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil }
	options := plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: authorize, Connect: connect}
	if c {
		g, err := r.Guest(author.Options{Authorize: authorize})
		if err != nil {
			t.Fatal(err)
		}
		backend, _ = inprocess.New(g)
		open = func() (author.Caller, error) {
			s, err := Open(context.Background(), r.Descriptor(), options)
			if s != nil {
				closers = append(closers, func() { s.Abort() })
			}
			return s, err
		}
	} else {
		g, err := r.Guest(author.Options{Authorize: authorize})
		if err != nil {
			t.Fatal(err)
		}
		backend, _ = inprocess.New(g)
		open = func() (author.Caller, error) {
			s, err := plugin.Open(context.Background(), r.Descriptor(), options)
			if s != nil {
				closers = append(closers, func() { s.Abort() })
			}
			return s, err
		}
	}
	caller, err := open()
	if err != nil {
		t.Fatal(err)
	}
	return caller, acquire, func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
}

// TestCapabilityBehaviorMatchesGo runs the same configuration and health calls
// over the Go and the C-backed stacks and compares every outcome.
func TestCapabilityBehaviorMatchesGo(t *testing.T) {
	scope := func(_ context.Context, r plugin.Request, id string) (string, error) {
		if id == "forbidden" {
			return "", errors.New("no")
		}
		if id == "empty" {
			return "", nil
		}
		return "tenant/" + id, nil
	}
	health := map[string]func(context.Context) (capability.Health, error){
		"healthy": func(context.Context) (capability.Health, error) {
			return capability.Health{Status: "healthy", Ready: true}, nil
		},
		"degraded": func(context.Context) (capability.Health, error) { return capability.Health{Status: "degraded"}, nil },
		"bad-enum": func(context.Context) (capability.Health, error) {
			return capability.Health{Status: "great", Ready: true}, nil
		},
		"error": func(context.Context) (capability.Health, error) { return capability.Health{}, errors.New("db down") },
		"remote": func(context.Context) (capability.Health, error) {
			return capability.Health{}, &plugin.RemoteError{Code: "maintenance", Message: "later"}
		},
	}
	configure := func(ctx context.Context, caller author.Caller, instanceID, revision, values string) string {
		out, err := author.Call(ctx, caller, capability.ConfigurationMethod(), capability.Configuration{Instance: instanceID, Revision: revision, Values: json.RawMessage(values)})
		if err != nil {
			return "config:" + describe(err)
		}
		return fmt.Sprintf("config:ok %s@%s", out.Instance, out.Revision)
	}
	for name, h := range health {
		t.Run("health-"+name, func(t *testing.T) {
			var outs [2]string
			for i, c := range []bool{false, true} {
				caller, _, done := capabilityRig(t, c, h, scope)
				got, err := author.Call(context.Background(), caller, capability.HealthMethod(), capability.HealthRequest{})
				if err != nil {
					outs[i] = "health:" + describe(err)
				} else {
					outs[i] = fmt.Sprintf("health:%+v", got)
				}
				done()
			}
			if outs[0] != outs[1] {
				t.Fatalf("differs\n  go: %s\n  c:  %s", outs[0], outs[1])
			}
		})
	}
	steps := []struct{ id, revision, values string }{
		{"one", "r1", `{"name":"a"}`},
		{"one", "r1", `{"name":"b"}`},           // reused revision, new values
		{"one", "r2", `{"name":"b","count":3}`}, // replacement
		{"one", "r3", `{"unknown":1}`},          // schema violation
		{"one", "r4", `{"name":1}`},
		{"one", "r5", `{`}, // malformed
		{"one", "r6", `null`},
		{"one", "r7", `[]`},
		{"", "r1", `{"name":"a"}`},
		{"one", "", `{"name":"a"}`},
		{"forbidden", "r1", `{"name":"a"}`},
		{"empty", "r1", `{"name":"a"}`},
		{"two", "r1", `{"name":"a"}`},
		{"three", "r1", `{"name":"a"}`}, // over capacity of two
		{"one", "r8", `{"name":"a","count":1.5}`},
	}
	t.Run("configuration-sequence", func(t *testing.T) {
		var outs [2]string
		for i, c := range []bool{false, true} {
			caller, acquire, done := capabilityRig(t, c, health["healthy"], scope)
			var lines []string
			for _, s := range steps {
				lines = append(lines, configure(context.Background(), caller, s.id, s.revision, s.values))
			}
			lines = append(lines, acquire("tenant/one"), acquire("tenant/two"), acquire("tenant/three"), acquire("tenant/forbidden"))
			outs[i] = fmt.Sprintf("%q", lines)
			done()
		}
		if outs[0] != outs[1] {
			t.Fatalf("differs\n  go: %s\n  c:  %s", outs[0], outs[1])
		}
	})
}
