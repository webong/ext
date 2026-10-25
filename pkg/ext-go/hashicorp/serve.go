package hashicorp

import (
	"github.com/hashicorp/go-hclog"
	hc "github.com/hashicorp/go-plugin"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// Serve serves guest as a go-plugin plugin on this process's stdio and returns
// when the host disconnects. protocol is "grpc" or "netrpc" and must match what
// the host allows. Call it once from the plugin's main.
//
// go-plugin owns the handshake and the connection. Its logger is silenced:
// stdout carries the handshake and stderr is the diagnostic channel, so the
// guest writes there itself. A plugin author needs no go-plugin or go-hclog
// import; a program that wants other go-plugin options builds its own
// hc.ServeConfig from HandshakeConfig, PluginName, Plugin and GRPCServer.
func Serve(guest plugin.Endpoint, protocol string) error {
	if missingEndpoint(guest) {
		return plugin.ErrDenied
	}
	if protocol != "grpc" && protocol != "netrpc" {
		return plugin.ErrUnsupported
	}
	config := &hc.ServeConfig{
		HandshakeConfig: HandshakeConfig(),
		Plugins:         hc.PluginSet{PluginName: &Plugin{Guest: guest}},
		Logger:          hclog.NewNullLogger(),
	}
	if protocol == "grpc" {
		config.GRPCServer = GRPCServer
	}
	hc.Serve(config)
	return nil
}
