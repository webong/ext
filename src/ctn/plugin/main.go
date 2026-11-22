// This is the private entrypoint of the installed CTN ext package.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
	"github.com/webong/ext/src/ctn"
)

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "ctn package:", err)
		os.Exit(1)
	}
}

func serve() error {
	address, token := os.Getenv("EXT_COMMAND_ADDR"), os.Getenv("EXT_COMMAND_TOKEN")
	if address == "" || len(token) != 64 {
		return fmt.Errorf("command guest requires an ext connection")
	}
	d := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity: plugin.Identity{
			ID: os.Getenv("EXT_COMMAND_ID"), Revision: os.Getenv("EXT_COMMAND_REVISION"), Version: os.Getenv("EXT_COMMAND_VERSION"),
		},
		Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "ext.command", Version: "v1"}, Operations: []plugin.Operation{{Name: "run", Progress: true}}}},
	}
	guest, err := jsonline.NewFuncGuest(d, func(_ context.Context, method string, payload json.RawMessage) (any, error) {
		if method != "ext.command.run" {
			return nil, fmt.Errorf("unknown command method")
		}
		var request struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			return nil, err
		}
		if request.Command != "ctn" {
			return nil, fmt.Errorf("command %q is not served by CTN", request.Command)
		}
		return struct {
			Exit int `json:"exit"`
		}{ctn.Run(request.Args, os.Stdout, os.Stderr)}, nil
	}, jsonline.FuncOptions{})
	if err != nil {
		return err
	}
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(conn, token+"\n"); err != nil {
		_ = conn.Close()
		return err
	}
	return jsonline.ServeGuest(context.Background(), conn, guest)
}
