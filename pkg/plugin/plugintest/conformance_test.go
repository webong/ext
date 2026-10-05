package plugintest_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/inprocess"
	"github.com/webong/ctx/pkg/plugin/jsonline"
	"github.com/webong/ctx/pkg/plugin/plugintest"
)

func TestBindings(t *testing.T) {
	t.Run("inprocess", func(t *testing.T) {
		plugintest.Run(t, func(context.Context) (plugin.Backend, error) {
			g, err := plugintest.Guest()
			if err != nil {
				return nil, err
			}
			return inprocess.New(g)
		})
	})
	t.Run("jsonline", func(t *testing.T) {
		plugintest.Run(t, func(context.Context) (plugin.Backend, error) {
			g, err := plugintest.Guest()
			if err != nil {
				return nil, err
			}
			a, b := net.Pipe()
			go func() { _ = jsonline.ServeGuest(context.Background(), b, g) }()
			return jsonline.NewClient(a), nil
		})
	})
}
func TestFrozenV1Fixtures(t *testing.T) {
	data, err := os.ReadFile("../testdata/v1/descriptor.json")
	if err != nil {
		t.Fatal(err)
	}
	var d plugin.Descriptor
	if err := plugin.Decode(data, &d); err != nil {
		t.Fatal(err)
	}
	if err := plugin.MatchHandshake(d, plugintest.Descriptor()); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("../testdata/v1/invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	var invalid []string
	if err := json.Unmarshal(data, &invalid); err != nil {
		t.Fatal(err)
	}
	for _, input := range invalid {
		var raw json.RawMessage
		if plugin.Decode([]byte(input), &raw) == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
