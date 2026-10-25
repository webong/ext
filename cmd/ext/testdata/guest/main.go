// Command guest is a package that provides two commands to the ext end-to-end test.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
)

func main() {
	d := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/hello", Revision: "child", Version: "1.0.0"},
		Contracts:  []plugin.Contract{pkgcmd.RunContract},
	}
	err := jsonline.ServeFunc(context.Background(), d, func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
		var req struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		say := func(stream, text string) {
			_ = plugin.ProgressFrom(ctx).Report(plugin.Progress{Kind: "log", Stream: stream, Text: text})
		}
		switch req.Command {
		case "hi":
			say("out", strings.Join(req.Args, " ")+"\n")
			return map[string]int{"exit": 0}, nil
		case "bye":
			say("err", "bye\n")
			return map[string]int{"exit": 4}, nil
		}
		return nil, fmt.Errorf("unknown command %q", req.Command)
	}, jsonline.FuncOptions{})
	if err != nil {
		os.Exit(1)
	}
}
