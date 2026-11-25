// Package wasm binds the protocol-neutral res/web/host WebAssembly runtime to the
// ext plugin engine.
package wasm

import (
	"context"

	plugin "github.com/webong/ext/pkg/ext-go/core/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
	webwasm "github.com/webong/ext/res/web/host/engine/wasm"
)

const MaxModuleBytes = webwasm.MaxModuleBytes
const DefaultMemoryPages = webwasm.DefaultMemoryPages

type Options = webwasm.Options

// CompilationCache keeps compiled modules, so opening the same module again skips
// compilation: set it in Options.CompilationCache. Share one among the sessions of a
// process and Close it after they have closed.
type CompilationCache = webwasm.CompilationCache

// NewCompilationCache returns an empty cache that lives in memory.
func NewCompilationCache() *CompilationCache { return webwasm.NewCompilationCache() }

type Backend struct {
	*jsonline.Client
	command *webwasm.Command
}

// Open starts a persistent WASI command and binds its raw byte stream to the
// ext.plugin/v1 JSON-line transport.
// A guest SDK compiles its reactor exports into every wasm32 module, so a WASI
// command may import ext_plugin.reverse without ever calling it: Open provides the
// reactor ABI's stubs unless opts names its own.
func Open(ctx context.Context, module []byte, opts Options) (*Backend, error) {
	if opts.StubImports == nil {
		opts.StubImports = reactorABI.StubImports
	}
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
	return plugin.BackendProfile{Name: "wasm/wasi-preview1", Protocols: []string{plugin.APIVersion}, Cancellation: "module", ProcessOwner: "wazero", Boundary: plugin.BoundaryRuntime}
}
