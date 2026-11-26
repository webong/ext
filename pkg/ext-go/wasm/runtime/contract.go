// Package runtime implements the res/web WebAssembly host contract with
// wazero. It does not assign an Ext plugin protocol to guest byte streams.
package runtime

import (
	"context"

	contract "github.com/webong/ext/res/web/host/engine/wasm"
)

const MaxModuleBytes = contract.MaxModuleBytes
const DefaultMemoryPages = contract.DefaultMemoryPages

var (
	ErrInvalid     = contract.ErrInvalid
	ErrUnsupported = contract.ErrUnsupported
	ErrMismatch    = contract.ErrMismatch
	ErrClosed      = contract.ErrClosed
)

type Options = contract.Options
type CommandOptions = contract.CommandOptions
type Inspection = contract.Inspection
type Target = contract.Target
type ReactorABI = contract.ReactorABI
type StubImport = contract.StubImport
type ReactorOptions = contract.ReactorOptions

const (
	TargetNone      = contract.TargetNone
	TargetWASI      = contract.TargetWASI
	TargetHost      = contract.TargetHost
	TargetComponent = contract.TargetComponent
)

// Engine is a wazero implementation of the runtime-neutral web contract.
type Engine struct{}

func (Engine) NewCompilationCache() contract.CompilationCache { return NewCompilationCache() }
func (Engine) Open(ctx context.Context, module []byte, options Options) (contract.Command, error) {
	return Open(ctx, module, options)
}
func (Engine) RunCommand(ctx context.Context, module []byte, options CommandOptions) (int, error) {
	return RunCommand(ctx, module, options)
}
func (Engine) Inspect(ctx context.Context, module []byte) (Inspection, error) {
	return Inspect(ctx, module)
}
func (Engine) OpenReactor(ctx context.Context, module []byte, abi ReactorABI, options ReactorOptions) (contract.Reactor, error) {
	return OpenReactor(ctx, module, abi, options)
}

var _ contract.Runtime = Engine{}
var _ contract.Command = (*Command)(nil)
var _ contract.Reactor = (*Reactor)(nil)
