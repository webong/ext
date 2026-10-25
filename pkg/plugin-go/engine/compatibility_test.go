package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/plugintest"
	"github.com/webong/ext/pkg/plugin-go/engine/transport/inprocess"
	"testing"
)

func TestNegotiationAndObserver(t *testing.T) {
	v, err := plugin.NegotiateProtocol([]string{"ext.plugin/v2", plugin.APIVersion}, []string{plugin.APIVersion})
	if err != nil || v != plugin.APIVersion {
		t.Fatal(v, err)
	}
	if _, err := plugin.NegotiateProtocol([]string{"ext.plugin/v2"}, []string{plugin.APIVersion}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatal(err)
	}
	d := plugintest.Descriptor()
	g, _ := plugintest.Guest()
	var events []plugin.Event
	s, err := plugin.Open(context.Background(), d, plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: func(context.Context, plugin.Request) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return inprocess.New(g) }, Observer: func(_ context.Context, e plugin.Event) { events = append(events, e) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	if _, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "echo", json.RawMessage(`{"private":"payload"}`)); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[3].Stage != "invoke" || events[3].Code != "ok" {
		t.Fatal(events)
	}
}
