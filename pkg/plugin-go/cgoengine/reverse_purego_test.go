//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package cgoengine

import (
	"context"
	"encoding/json"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/transport/inprocess"
)

// The pure-Go build serves reverse calls and progress through pkg/plugin-go
// unchanged, so a program can use them with the engine API alone.
func TestPureGoSessionServesReverseCallsAndProgress(t *testing.T) {
	svc := plugin.ContractRef{Name: "host.svc", Version: "v1"}
	work := plugin.ContractRef{Name: "work", Version: "v1"}
	d := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "guest", Revision: "r1"},
		Contracts:  []plugin.Contract{{ContractRef: work, Operations: []plugin.Operation{{Name: "long", Progress: true}}}},
		Reverse:    []plugin.Contract{{ContractRef: svc, Operations: []plugin.Operation{{Name: "sign"}}}},
	}
	guest, err := NewGuest(d, plugin.GuestOptions{Handler: func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		_ = plugin.ProgressFrom(ctx).Report(plugin.Progress{Kind: "log", Text: "working"})
		return plugin.ReverseFrom(ctx).Call(ctx, svc, "sign", json.RawMessage(`1`))
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := inprocess.New(guest)
	if err != nil {
		t.Fatal(err)
	}
	session, err := Open(context.Background(), d, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Reverse: &plugin.ReverseOptions{
			Handle: func(context.Context, plugin.Request) (json.RawMessage, error) {
				return json.RawMessage(`"signed"`), nil
			},
			Authorize: func(context.Context, plugin.Request) error { return nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	var texts []string
	got, err := session.CallProgress(context.Background(), work, "long", nil, func(p plugin.Progress) { texts = append(texts, p.Text) })
	if err != nil || string(got) != `"signed"` || len(texts) != 1 || texts[0] != "working" {
		t.Fatalf("%s %v %q", got, err, texts)
	}
}
