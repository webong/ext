// Package echo contains the single typed implementation shared by all runtime
// examples. It has no dependency on a loader or product adapter.
package echo

import (
	"context"
	goengine "github.com/webong/ext/pkg/plugin-go/cengine"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/author"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/inprocess"
	"github.com/webong/ext/pkg/plugin-go/contract/validate/schema"
)

type Message struct {
	Message string `json:"message"`
}

func Method() author.Method[Message, Message] {
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"message": {Type: "string", MaxLength: 256}}, Required: []string{"message"}}
	return author.Method[Message, Message]{Contract: goengine.ContractRef{Name: "example.echo", Version: "v1"}, Operation: goengine.Operation{Name: "echo", Surface: "observation"}, Input: &s, Output: &s}
}

func Descriptor() goengine.Descriptor {
	m := Method()
	return goengine.Descriptor{APIVersion: goengine.APIVersion, Identity: goengine.Identity{ID: "example/runtime-echo", Revision: "compiled-1"}, Contracts: []goengine.Contract{{ContractRef: m.Contract, Operations: []goengine.Operation{m.Operation}}}}
}

// New is also the native Go and C shared-library factory signature. The startup
// context must not be retained as the guest's lifetime context.
func New(ctx context.Context) (goengine.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := author.New(Descriptor().Identity)
	if err != nil {
		return nil, err
	}
	if err := author.Register(r, Method(), func(_ context.Context, _ goengine.Request, m Message) (Message, error) { return m, nil }); err != nil {
		return nil, err
	}
	g, err := r.Guest(author.Options{Authorize: func(_ context.Context, request goengine.Request) error {
		return goengine.ValidateRequest(Descriptor(), request)
	}})
	if err != nil {
		return nil, err
	}
	return inprocess.New(g)
}
