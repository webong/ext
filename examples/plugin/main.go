// This example hosts a consumer-owned contract over an in-memory duplex
// connection. Real hosts verify installed artifacts and use their own policy,
// endpoint authentication, and supervisor before opening a plugin session.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/jsonline"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	d := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/catalog", Revision: "embedded-example-1", Version: "1.0.0"},
		Contracts:  []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.catalog", Version: "v1"}, Operations: []plugin.Operation{{Name: "inspect", Surface: "observation"}}}},
	}
	trusted := d.Clone() // This example's reviewed, compiled-in implementation.
	ctx := context.Background()
	host, provider := net.Pipe()
	defer host.Close()
	done := make(chan error, 1)
	go func() {
		done <- jsonline.Serve(ctx, provider, d, func(_ context.Context, request plugin.Request) (json.RawMessage, error) {
			if request.Operation != "inspect" {
				return nil, plugin.ErrDenied
			}
			return json.RawMessage(`{"items":["example"]}`), nil
		})
	}()
	session, err := plugin.Open(ctx, d, plugin.Options{
		Verify: func(_ context.Context, candidate plugin.Descriptor) error {
			return plugin.MatchHandshake(trusted, candidate)
		},
		Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return jsonline.NewClient(host), nil },
		Authorize: func(_ context.Context, request plugin.Request) error {
			if request.Operation != "inspect" || request.Surface != "observation" {
				return plugin.ErrDenied
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	defer session.Abort()
	result, err := session.Call(ctx, d.Contracts[0].ContractRef, "inspect", json.RawMessage(`{}`))
	if err != nil {
		return err
	}
	fmt.Println(string(result))
	if err := session.Close(ctx); err != nil {
		return err
	}
	return <-done
}
