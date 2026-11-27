// Graph fixture: each private copy serves the descriptor beside its executable.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
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
	var manifest packagekit.Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		panic(err)
	}
	err = jsonline.ServeFunc(context.Background(), manifest.Descriptor, func(context.Context, string, json.RawMessage) (any, error) { return map[string]any{}, nil }, jsonline.FuncOptions{})
	if err != nil {
		os.Exit(1)
	}
}
