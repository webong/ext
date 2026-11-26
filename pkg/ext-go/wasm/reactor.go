package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/core/plugin"
	"github.com/webong/ext/pkg/ext-go/wasm/runtime"
	webwasm "github.com/webong/ext/res/web/host/engine/wasm"
)

const (
	reactorABIVersion = 1
	reactorHandshake  = 1
	reactorInvoke     = 2
	reactorOK         = 0
	reactorInvalid    = 1
	reactorClosed     = 2
)

type ReactorOptions = webwasm.ReactorOptions

var reactorABI = webwasm.ReactorABI{
	Version: reactorABIVersion, MaxMessageBytes: plugin.MaxFrameBytes, SuccessStatus: reactorOK,
	VersionExport: "ext_plugin_guest_abi_version", OpenExport: "ext_plugin_guest_open",
	CallExport: "ext_plugin_guest_call", CloseExport: "ext_plugin_guest_close",
	AllocateExport: "ext_plugin_guest_alloc", FreeExport: "ext_plugin_guest_free",
	InitializeExport: "_initialize", DisallowedExport: "_start",
	// A reactor may call its host through ext_plugin.reverse
	// (docs/plugin-reactor-abi.md); this host carries no reverse calls yet, so it
	// answers 2, unavailable, which the guest reports as reverse_unavailable.
	StubImports: []webwasm.StubImport{{Module: "ext_plugin", Name: "reverse", Params: 5, Result: 2}},
}

type ReactorBackend struct {
	reactor *runtime.Reactor
}

func OpenReactor(ctx context.Context, module []byte, opts ReactorOptions) (*ReactorBackend, error) {
	reactor, err := runtime.OpenReactor(ctx, module, reactorABI, opts)
	if err != nil {
		return nil, engineError(err)
	}
	return &ReactorBackend{reactor: reactor}, nil
}

func (b *ReactorBackend) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	var descriptor plugin.Descriptor
	data, err := b.exchange(ctx, reactorHandshake, struct {
		Deadline time.Time `json:"deadline"`
	}{deadline})
	if err == nil {
		err = plugin.Decode(data, &descriptor)
	}
	if err == nil {
		err = descriptor.Validate()
	}
	if err != nil {
		_ = b.Close()
	}
	return descriptor, err
}

func (b *ReactorBackend) Invoke(ctx context.Context, request plugin.Request) (plugin.Response, error) {
	if !request.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, request.Deadline)
		defer cancel()
	}
	var response plugin.Response
	data, err := b.exchange(ctx, reactorInvoke, request)
	if err == nil {
		err = plugin.Decode(data, &response)
	}
	if err == nil {
		err = response.Validate(request.ID)
	}
	if err != nil {
		_ = b.Close()
	}
	return response, err
}

func (b *ReactorBackend) exchange(ctx context.Context, operation uint32, value any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
	request, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(request) == 0 || len(request) > plugin.MaxFrameBytes {
		return nil, plugin.ErrInvalid
	}
	status, response, err := b.reactor.Call(ctx, operation, request)
	if err != nil {
		return nil, engineError(err)
	}
	switch status {
	case reactorOK:
		return response, nil
	case reactorClosed:
		_ = b.Close()
		return nil, plugin.ErrClosed
	case reactorInvalid:
		return nil, plugin.ErrInvalid
	default:
		_ = b.Close()
		return nil, errors.New("plugin: WebAssembly reactor call failed")
	}
}

func (b *ReactorBackend) Close() error {
	return engineError(b.reactor.Close())
}

func (b *ReactorBackend) WaitClosed(ctx context.Context) error {
	return engineError(b.reactor.WaitClosed(ctx))
}

func ReactorProfile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "wasm/reactor", Protocols: []string{plugin.APIVersion}, Cancellation: "module", ProcessOwner: "wazero", Boundary: plugin.BoundaryRuntime}
}

var _ plugin.Backend = (*Backend)(nil)
var _ plugin.Backend = (*ReactorBackend)(nil)
