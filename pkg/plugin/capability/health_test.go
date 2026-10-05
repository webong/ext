package capability_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/author"
	"github.com/webong/ctx/pkg/plugin/capability"
	"github.com/webong/ctx/pkg/plugin/inprocess"
	"github.com/webong/ctx/pkg/plugin/instance"
	"github.com/webong/ctx/pkg/plugin/schema"
)

func TestConfigurationAndHealth(t *testing.T) {
	ctx := context.Background()
	r, _ := author.New(plugin.Identity{ID: "example/capability", Revision: "r1"})
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"name": {Type: "string"}}, Required: []string{"name"}}
	manager, err := instance.New(instance.Options[string]{Capacity: 4, Validate: func(raw json.RawMessage) error { return s.Check(raw) }, Create: func(_ context.Context, _ string, raw json.RawMessage) (string, func() error, error) {
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
	g, _ := r.Guest(author.Options{Authorize: func(context.Context, plugin.Request) error { return nil }})
	b, _ := inprocess.New(g)
	session, err := plugin.Open(ctx, r.Descriptor(), plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: func(context.Context, plugin.Request) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }})
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
}
