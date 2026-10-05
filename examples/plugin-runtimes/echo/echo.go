// Package echo contains the single typed implementation shared by all runtime
// examples. It has no dependency on a loader or product adapter.
package echo

import (
	"context"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/author"
	"github.com/webong/ctx/pkg/plugin/inprocess"
	"github.com/webong/ctx/pkg/plugin/schema"
)

type Message struct {
	Message string `json:"message"`
}

func Method() author.Method[Message, Message] {
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"message": {Type: "string", MaxLength: 256}}, Required: []string{"message"}}
	return author.Method[Message, Message]{Contract: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operation: plugin.Operation{Name: "echo", Surface: "observation"}, Input: &s, Output: &s}
}

func Descriptor() plugin.Descriptor {
	m := Method()
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/runtime-echo", Revision: "compiled-1"}, Contracts: []plugin.Contract{{ContractRef: m.Contract, Operations: []plugin.Operation{m.Operation}}}}
}

// New is also the native Go and C shared-library factory signature. The startup
// context must not be retained as the guest's lifetime context.
func New(ctx context.Context) (plugin.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := author.New(Descriptor().Identity)
	if err != nil {
		return nil, err
	}
	if err := author.Register(r, Method(), func(_ context.Context, _ plugin.Request, m Message) (Message, error) { return m, nil }); err != nil {
		return nil, err
	}
	g, err := r.Guest(author.Options{Authorize: func(_ context.Context, request plugin.Request) error {
		return plugin.ValidateRequest(Descriptor(), request)
	}})
	if err != nil {
		return nil, err
	}
	return inprocess.New(g)
}
