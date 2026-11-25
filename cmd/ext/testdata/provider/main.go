// Provider is deliberately not an ext.command plugin.
package main

import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
	"os"
	"strconv"
	"time"
)

var providerID = "example/provider"
var marker string

func main() {
	if marker != "" {
		if executable, err := os.Executable(); err == nil {
			_ = os.WriteFile(marker+".executable", []byte(executable), 0600)
		}
		_ = os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	d := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: providerID, Revision: "provider-v1", Version: "1.0.0"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.value", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}}
	if err := jsonline.ServeFunc(context.Background(), d, func(_ context.Context, _ string, payload json.RawMessage) (any, error) {
		var input struct{ Wait bool }
		_ = json.Unmarshal(payload, &input)
		if input.Wait {
			if marker != "" {
				_ = os.WriteFile(marker+".waiting", []byte("waiting"), 0600)
			}
			time.Sleep(30 * time.Second)
		}
		return map[string]string{"value": "from-provider"}, nil
	}, jsonline.FuncOptions{}); err != nil {
		os.Exit(1)
	}
}
