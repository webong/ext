// The reference conformance guest (plugintest.Guest and
// lib/plugin/testdata/v1/descriptor.json), written as a go-source guest: the
// backend interprets this file; it is never compiled.
package main

import (
	"context"
	"encoding/json"
	"errors"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/inprocess"
)

func descriptor() plugin.Descriptor {
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "ctx/conformance", Revision: "fixture-1"},
		Contracts: []plugin.Contract{{
			ContractRef: plugin.ContractRef{Name: "ext.conformance", Version: "v1"},
			Operations:  []plugin.Operation{{Name: "echo"}, {Name: "wait"}, {Name: "private-error"}, {Name: "public-error"}},
		}},
	}
}

func handle(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
	switch r.Operation {
	case "wait":
		<-ctx.Done()
		return nil, ctx.Err()
	case "private-error":
		return nil, errors.New("secret diagnostic")
	case "public-error":
		return nil, &plugin.RemoteError{Code: "busy", Message: "try later", RetryAfterMilliseconds: 10}
	default:
		return r.Payload, nil
	}
}

func ExtPlugin(ctx context.Context) (plugin.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := plugin.NewGuest(descriptor(), plugin.GuestOptions{Handler: handle})
	if err != nil {
		return nil, err
	}
	return inprocess.New(g)
}

func main() {}
