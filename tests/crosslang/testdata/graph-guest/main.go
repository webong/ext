// Graph fixture: each private copy serves the descriptor beside its executable.
package main

import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
	"os"
	"path/filepath"
)

func main() {
	path, err := os.Executable()
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "plugin.json"))
	if err != nil {
		panic(err)
	}
	var manifest struct {
		Descriptor plugin.Descriptor `json:"descriptor"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		panic(err)
	}
	err = jsonline.ServeFunc(context.Background(), manifest.Descriptor, func(context.Context, string, json.RawMessage) (any, error) { return map[string]any{}, nil }, jsonline.FuncOptions{})
	if err != nil {
		os.Exit(1)
	}
}
