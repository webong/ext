// This is the private entrypoint of the installed CTO ext package.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
	"github.com/webong/ext/src/cto"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "--ext-cto-exec" {
		os.Exit(cto.RunAs(os.Args[2], os.Args[3:], os.Stdout, os.Stderr))
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "cto package:", err)
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
		if request.Command != "cto" && request.Command != "sandbox" {
			return nil, fmt.Errorf("command %q is not served by CTO", request.Command)
		}
		name := "cto"
		if request.Command == "sandbox" {
			name = "ext sandbox"
		}
		return struct {
			Exit int `json:"exit"`
		}{run(name, request.Args, os.Stdout, os.Stderr)}, nil
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

func run(name string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "exec" {
		return cto.RunAs(name, args, stdout, stderr)
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "cto:", err)
		return 1
	}
	child := exec.Command(self, append([]string{"--ext-cto-exec", name}, args...)...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, stdout, stderr
	child.Env = os.Environ()
	files, err := attachFDs(child, args, []string{"--launch-fd", "--status-fd"})
	if err != nil {
		fmt.Fprintln(stderr, "cto:", err)
		return 1
	}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	err = child.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	fmt.Fprintln(stderr, "cto:", err)
	return 1
}
