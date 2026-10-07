// Package hashicorp binds CTX's shared plugin endpoint to HashiCorp go-plugin.
// This library backend owns go-plugin's handshake, RPC bindings, dispensing,
// and client cleanup. Domain semantics remain in the consumer's plugin.Guest.
package hashicorp

import (
	"context"
	"errors"
	"net/rpc"
	"reflect"
	"sync"

	hc "github.com/hashicorp/go-plugin"
	"github.com/webong/ext/pkg/plugin"
	"google.golang.org/grpc"
)

const PluginName = "ctx"

// HandshakeConfig returns a copy of the backend's native handshake. The cookie
// identifies a compatible launch convention; it provides no authentication.
func HandshakeConfig() hc.HandshakeConfig {
	return hc.HandshakeConfig{ProtocolVersion: 1, MagicCookieKey: "CTX_GO_PLUGIN", MagicCookieValue: plugin.APIVersion}
}

// Plugin implements both go-plugin bindings. Hosts leave Guest nil; plugins
// supply a shared Endpoint (a Guest or a verified bridge Relay). Configure AllowedProtocols explicitly on the host and
// use GRPCServer on the guest to select gRPC. Each dispensed object is a Backend.
type Plugin struct{ Guest plugin.Endpoint }

// Preserve fail-closed behavior for typed nil guests as the field now accepts
// arbitrary Endpoint implementations, including relay endpoints.
func missingEndpoint(endpoint plugin.Endpoint) bool {
	if endpoint == nil {
		return true
	}
	value := reflect.ValueOf(endpoint)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}

var _ hc.Plugin = (*Plugin)(nil)
var _ hc.GRPCPlugin = (*Plugin)(nil)

func (p *Plugin) Server(*hc.MuxBroker) (interface{}, error) {
	if missingEndpoint(p.Guest) {
		return nil, plugin.ErrDenied
	}
	return &rpcServer{guest: p.Guest}, nil
}

func (*Plugin) Client(_ *hc.MuxBroker, client *rpc.Client) (interface{}, error) {
	if client == nil {
		return nil, plugin.ErrInvalid
	}
	return &rpcBackend{client: client}, nil
}

func (p *Plugin) GRPCServer(_ *hc.GRPCBroker, server *grpc.Server) error {
	if missingEndpoint(p.Guest) {
		return plugin.ErrDenied
	}
	server.RegisterService(&runtimeService, &grpcGuest{guest: p.Guest})
	return nil
}

func (*Plugin) GRPCClient(ctx context.Context, _ *hc.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	if conn == nil {
		return nil, plugin.ErrInvalid
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &grpcBackend{conn: conn, lifetime: lifetime, cancel: cancel}, nil
}

// Connect starts/connects a dedicated go-plugin client and dispenses the CTX
// interface. Call it inside plugin.Options.Connect, after trust verification.
// Ownership transfers here: failures kill the client, and the returned backend
// kills it on Close. Never also supervise that child with pkg/graph/supervisor.
// Configure ClientConfig.StartTimeout: cancellation returns promptly, but
// cleanup of a pending native startup waits for go-plugin's startup to return.
func Connect(ctx context.Context, client *hc.Client) (plugin.Backend, error) {
	return ConnectInterface(ctx, client, PluginName, func(value interface{}) (plugin.Backend, error) {
		backend, ok := value.(plugin.Backend)
		if !ok {
			return nil, errors.New("go-plugin did not dispense a CTX backend")
		}
		return backend, nil
	})
}

// ConnectInterface supports an existing go-plugin interface with a
// consumer-supplied translation to Backend. The translation owns its domain
// contract and must honor Backend cancellation and handshake requirements.
// It shares Connect's dedicated-client ownership and cleanup behavior.
func ConnectInterface(ctx context.Context, client *hc.Client, name string, bind func(interface{}) (plugin.Backend, error)) (plugin.Backend, error) {
	if client == nil {
		return nil, plugin.ErrInvalid
	}
	if name == "" || bind == nil {
		client.Kill()
		return nil, plugin.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		client.Kill()
		return nil, err
	}
	type result struct {
		backend plugin.Backend
		err     error
	}
	ready := make(chan result)
	go func() {
		protocol, err := client.Client()
		var backend plugin.Backend
		if err == nil {
			var value interface{}
			value, err = protocol.Dispense(name)
			if err == nil {
				backend, err = bind(value)
				if err == nil && backend == nil {
					err = plugin.ErrInvalid
				}
			}
		}
		select {
		case ready <- result{backend, err}:
		case <-ctx.Done():
			if backend != nil {
				_ = backend.Close()
			}
			client.Kill()
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ready:
		if result.err != nil {
			if result.backend != nil {
				_ = result.backend.Close()
			}
			client.Kill()
			return nil, result.err
		}
		backend := &ownedBackend{Backend: result.backend, client: client}
		if err := ctx.Err(); err != nil {
			_ = backend.Close()
			return nil, err
		}
		return backend, nil
	}
}

type ownedBackend struct {
	plugin.Backend
	client *hc.Client
	once   sync.Once
	err    error
}

func (b *ownedBackend) Close() error {
	b.once.Do(func() { b.err = b.Backend.Close(); b.client.Kill() })
	return b.err
}
