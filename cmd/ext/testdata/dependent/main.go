// A guest whose implementation needs another installed provider.
package main

import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
	"os"
	"strconv"
)

var marker string

func main() {
	if marker != "" {
		if executable, err := os.Executable(); err == nil {
			_ = os.WriteFile(marker+".executable", []byte(executable), 0600)
		}
		_ = os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	value := plugin.ContractRef{Name: "example.value", Version: "v1"}
	d := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/dependent", Revision: "dependent-v1", Version: "1.0.0"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.dependent", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}, Reverse: []plugin.Contract{{ContractRef: value, Operations: []plugin.Operation{{Name: "read"}}}}}
	if err := jsonline.ServeFunc(context.Background(), d, func(ctx context.Context, _ string, input json.RawMessage) (any, error) {
		payload, err := plugin.ReverseFrom(ctx).Call(ctx, value, "read", input)
		if err != nil {
			return nil, err
		}
		return map[string]any{"via": "dependent", "result": payload}, nil
	}, jsonline.FuncOptions{}); err != nil {
		os.Exit(1)
	}
}
