// Fixture guest used by bridge interoperability tests.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/hashicorp/go-hclog"
	hc "github.com/hashicorp/go-plugin"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/bridge"
	"github.com/webong/ctx/pkg/plugin/hashicorp"
	"github.com/webong/ctx/pkg/plugin/jsonline"
	"github.com/webong/ctx/pkg/plugin/plugintest"
	"io"
	"os"
	"os/exec"
)

func main() {
	protocol := flag.String("protocol", "grpc", "")
	pidfile := flag.String("pid-file", "", "")
	reverse := flag.String("jsonline-guest", "", "")
	flag.Parse()
	if *pidfile != "" {
		if err := os.WriteFile(*pidfile, []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
			panic(err)
		}
	}
	g, err := plugintest.Guest()
	if err != nil {
		panic(err)
	}
	var endpoint plugin.Endpoint = g
	if *reverse != "" {
		r, err := bridge.Open(context.Background(), plugintest.Descriptor(), plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: func(context.Context, plugin.Request) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
			cmd := exec.Command(*reverse)
			cmd.Env = []string{}
			reader, err := cmd.StdoutPipe()
			if err != nil {
				return nil, err
			}
			writer, err := cmd.StdinPipe()
			if err != nil {
				return nil, err
			}
			if err = cmd.Start(); err != nil {
				return nil, err
			}
			return jsonline.NewClient(&conn{ReadCloser: reader, WriteCloser: writer, cmd: cmd}), nil
		}})
		if err != nil {
			panic(err)
		}
		defer r.Close()
		endpoint = r
	}
	config := &hc.ServeConfig{HandshakeConfig: hashicorp.HandshakeConfig(), Plugins: hc.PluginSet{hashicorp.PluginName: &hashicorp.Plugin{Guest: endpoint}}, Logger: hclog.NewNullLogger()}
	if *protocol == "grpc" {
		config.GRPCServer = hashicorp.GRPCServer
	}
	hc.Serve(config)
}

type conn struct {
	io.ReadCloser
	io.WriteCloser
	cmd *exec.Cmd
}

func (c *conn) Close() error {
	_ = c.WriteCloser.Close()
	_ = c.ReadCloser.Close()
	_ = c.cmd.Process.Kill()
	return c.cmd.Wait()
}
