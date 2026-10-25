package main

import (
	"context"
	"github.com/webong/ext/pkg/plugin-go/cgoengine"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/author"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	"github.com/webong/ext/pkg/plugin-go/contract/validate/schema"
	"os"
)

func serveGoGuest() error {
	type message struct {
		Message string `json:"message"`
	}
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"message": {Type: "string", MaxLength: 256}}, Required: []string{"message"}}
	r, err := author.New(cgoengine.Identity{ID: "example/typescript", Revision: "compiled-1"})
	if err != nil {
		return err
	}
	method := author.Method[message, message]{Contract: cgoengine.ContractRef{Name: "example.echo", Version: "v1"}, Operation: cgoengine.Operation{Name: "echo", Surface: "observation"}, Input: &s, Output: &s}
	if err := author.Register(r, method, func(_ context.Context, _ cgoengine.Request, m message) (message, error) { return m, nil }); err != nil {
		return err
	}
	g, err := r.Guest(author.Options{Authorize: func(_ context.Context, r cgoengine.Request) error {
		if r.Operation != "echo" {
			return cgoengine.ErrDenied
		}
		return nil
	}})
	if err != nil {
		return err
	}
	return jsonline.ServeGuest(context.Background(), &pipes{Reader: os.Stdin, Writer: os.Stdout, stop: func() { _ = os.Stdin.Close(); _ = os.Stdout.Close() }}, g)
}
