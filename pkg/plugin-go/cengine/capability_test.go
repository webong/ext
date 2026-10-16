//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/author"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/capability"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/inprocess"
	"github.com/webong/ext/pkg/plugin-go/contract/validate/schema"
	"testing"
)

// The capability helpers must work over the C engine end to end: a C-backed
// registry, C-owned instances and a C session.
func TestCapabilityHelpersOverCEngine(t *testing.T) {
	ctx := context.Background()
	r, err := NewRegistry(plugin.Identity{ID: "example/capability", Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"name": {Type: "string"}}, Required: []string{"name"}}
	manager, err := NewInstances(instance.Options[string]{Capacity: 4, Validate: func(raw json.RawMessage) error { return s.Check(raw) }, Create: func(_ context.Context, _ string, raw json.RawMessage) (string, func() error, error) {
		return string(raw), func() error { return nil }, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if err := capability.RegisterConfiguration(r, manager, s, func(_ context.Context, _ plugin.Request, id string) (string, error) { return "subject/" + id, nil }); err != nil {
		t.Fatal(err)
	}
	if err := capability.RegisterHealth(r, func(context.Context) (capability.Health, error) {
		return capability.Health{Status: "healthy", Ready: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	g, err := r.Guest(author.Options{Authorize: func(context.Context, plugin.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := inprocess.New(g)
	session, err := Open(ctx, r.Descriptor(), plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: func(context.Context, plugin.Request) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	if _, err := author.Call(ctx, session, capability.ConfigurationMethod(), capability.Configuration{Instance: "one", Revision: "r1", Values: json.RawMessage(`{"name":"value"}`)}); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire("subject/one")
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Release()
	health, err := author.Call(ctx, session, capability.HealthMethod(), capability.HealthRequest{})
	if err != nil || !health.Ready {
		t.Fatal(health, err)
	}
	if _, err := author.Call(ctx, session, capability.ConfigurationMethod(), capability.Configuration{Instance: "one", Revision: "r2", Values: json.RawMessage(`{"unknown":"value"}`)}); err == nil {
		t.Fatal("invalid config accepted")
	}
	var nilManager *Instances[string]
	if err := capability.RegisterConfiguration(r, nilManager, s, func(context.Context, plugin.Request, string) (string, error) { return "x", nil }); err == nil {
		t.Fatal("typed nil manager accepted")
	}
}
