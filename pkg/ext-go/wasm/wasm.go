// Package wasm binds the protocol-neutral res/web WebAssembly runtime to the
// ext plugin engine.
package wasm

import (
	"context"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
	webwasm "github.com/webong/ext/res/web/wasm"
)

const MaxModuleBytes = webwasm.MaxModuleBytes
const DefaultMemoryPages = webwasm.DefaultMemoryPages

type Options = webwasm.Options

type Backend struct {
	*jsonline.Client
	command *webwasm.Command
}

// Open starts a persistent WASI command and binds its raw byte stream to the
// ext.plugin/v1 JSON-line transport.
func Open(ctx context.Context, module []byte, opts Options) (*Backend, error) {
	command, err := webwasm.Open(ctx, module, opts)
	if err != nil {
		return nil, engineError(err)
	}
	return &Backend{Client: jsonline.NewClient(command), command: command}, nil
}

func (b *Backend) Wait(ctx context.Context) error {
	return engineError(b.command.Wait(ctx))
}

func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "wasm/wasi-preview1", Protocols: []string{plugin.APIVersion}, Cancellation: "module", ProcessOwner: "wazero"}
}
