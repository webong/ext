//go:build ext_cengine && cgo && (darwin || linux || windows)

package plugin

import (
	"context"
	"errors"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"testing"
)

func TestSessionNamedProviderRequirement(t *testing.T) {
	d := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/provider", Revision: "r2"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.value", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}}
	for _, provider := range []string{"example/provider", "example/other"} {
		t.Run(provider, func(t *testing.T) {
			connected := false
			opts := plugin.Options{
				Authorize:    func(context.Context, plugin.Request) error { return nil },
				Requirements: []plugin.Requirement{{Contract: d.Contracts[0].ContractRef, Operation: "read", Provider: provider}},
				Verify:       func(context.Context, plugin.Descriptor) error { return nil },
				Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
					connected = true
					return nil, plugin.ErrDenied
				},
			}
			_, err := Open(context.Background(), d, opts)
			want := plugin.ErrNotFound
			if provider == d.Identity.ID {
				want = plugin.ErrDenied
			}
			if !errors.Is(err, want) || connected != (provider == d.Identity.ID) {
				t.Fatalf("err=%v connected=%v, want %v", err, connected, want)
			}
		})
	}
}
