// Package echo contains the single typed implementation shared by all runtime
// examples. It has no dependency on a loader or product adapter.
package echo

import (
	"context"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	"github.com/webong/ext/pkg/ext-go/engine/validate/schema"
)

type Message struct {
	Message string `json:"message"`
}

func Method() author.Method[Message, Message] {
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"message": {Type: "string", MaxLength: 256}}, Required: []string{"message"}}
	return author.Method[Message, Message]{Contract: cgoengine.ContractRef{Name: "example.echo", Version: "v1"}, Operation: cgoengine.Operation{Name: "echo", Surface: "observation"}, Input: &s, Output: &s}
}

func Descriptor() cgoengine.Descriptor {
	m := Method()
	return cgoengine.Descriptor{APIVersion: cgoengine.APIVersion, Identity: cgoengine.Identity{ID: "example/runtime-echo", Revision: "compiled-1"}, Contracts: []cgoengine.Contract{{ContractRef: m.Contract, Operations: []cgoengine.Operation{m.Operation}}}}
}

// New is also the native Go and C shared-library factory signature. The startup
// context must not be retained as the guest's lifetime context.
func New(ctx context.Context) (cgoengine.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := author.New(Descriptor().Identity)
	if err != nil {
		return nil, err
	}
	if err := author.Register(r, Method(), func(_ context.Context, _ cgoengine.Request, m Message) (Message, error) { return m, nil }); err != nil {
		return nil, err
	}
	g, err := r.Guest(author.Options{Authorize: func(_ context.Context, request cgoengine.Request) error {
		return cgoengine.ValidateRequest(Descriptor(), request)
	}})
	if err != nil {
		return nil, err
	}
	return inprocess.New(g)
}
