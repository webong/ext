package author_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/author"
	"github.com/webong/ext/pkg/plugin/inprocess"
	"github.com/webong/ext/pkg/plugin/schema"
)

type input struct {
	Value string `json:"value"`
}

func TestTypedRegistry(t *testing.T) {
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"value": {Type: "string", MinLength: 1}}, Required: []string{"value"}}
	m := author.Method[input, input]{Contract: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operation: plugin.Operation{Name: "echo", Surface: "observation"}, Input: &s, Output: &s}
	r, err := author.New(plugin.Identity{ID: "example/test", Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	if err := author.Register(r, m, func(_ context.Context, _ plugin.Request, i input) (input, error) { count++; return i, nil }); err != nil {
		t.Fatal(err)
	}
	if author.Register(r, m, func(context.Context, plugin.Request, input) (input, error) { return input{}, nil }) == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := r.Guest(author.Options{}); !errors.Is(err, plugin.ErrDenied) {
		t.Fatal(err)
	}
	guest, err := r.Guest(author.Options{Authorize: func(context.Context, plugin.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := inprocess.New(guest)
	session, err := plugin.Open(context.Background(), r.Descriptor(), plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: func(context.Context, plugin.Request) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	result, err := author.Call(context.Background(), session, m, input{Value: "hello"})
	if err != nil || result.Value != "hello" {
		t.Fatal(result, err)
	}
	for _, raw := range []string{`{}`, `{"value":""}`, `{"value":"ok","unexpected":true}`, `{"value":"ok","value":"again"}`} {
		if _, err := session.Call(context.Background(), m.Contract, "echo", json.RawMessage(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	if count != 1 {
		t.Fatalf("invalid requests reached handler: %d", count)
	}
	// New registrations cannot change an already-created guest.
	later := m
	later.Operation.Name = "later"
	if err := author.Register(r, later, func(_ context.Context, _ plugin.Request, i input) (input, error) { return i, nil }); err != nil {
		t.Fatal(err)
	}
	d, _ := guest.Handshake(context.Background())
	if len(d.Contracts[0].Operations) != 1 {
		t.Fatal("guest changed")
	}
}
