package wasm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/webong/ext/pkg/plugin"
)

// The reactor ABI is the ext.plugin C ABI v1 compiled to WebAssembly, plus the
// allocation exports that let a host place buffers in guest memory. The constants
// restate pkg/plugin-cshared/ext_plugin.h; the cross-language suite checks them.
const (
	reactorABIVersion = 1
	reactorHandshake  = 1
	reactorInvoke     = 2
	reactorOK         = 0
	reactorInvalid    = 1
	reactorClosed     = 2
)

// ReactorOptions configures OpenReactor.
type ReactorOptions struct {
	// MemoryLimitPages bounds guest linear memory (64 KiB/page). Zero uses 4096.
	// The response buffer alone is 384 pages (24 MiB).
	MemoryLimitPages uint32
	// Stderr receives guest diagnostics written to stdout or stderr; nil discards
	// them. A reactor has no stdin and nothing is inherited from the host.
	Stderr io.Writer
	// MaxStderrBytes caps forwarded diagnostics. Zero defaults to 64 KiB.
	MaxStderrBytes int64
}

// ReactorBackend hosts one WebAssembly reactor that exports the ext.plugin C ABI.
// Calls are serialized because a module instance is single-threaded. A call that
// outlives its deadline closes the module, so a late result is never delivered.
type ReactorBackend struct {
	runtime wazero.Runtime
	module  api.Module
	handle  uint64
	respPtr uint32
	lenPtr  uint32

	call, closeFn, alloc, free api.Function

	gate   chan struct{}
	closed chan struct{}
	done   chan struct{}
	once   sync.Once
	cancel context.CancelFunc
}

// OpenReactor compiles verified module bytes and instantiates a reactor: a module
// that exports memory, ext_plugin_abi_version, ext_plugin_open, ext_plugin_call,
// ext_plugin_close, ext_plugin_alloc and ext_plugin_free, and no _start. It checks
// the ABI version and opens one independent guest session. Use inside
// plugin.Options.Connect; ctx bounds compilation and startup, not the session.
func OpenReactor(ctx context.Context, module []byte, opts ReactorOptions) (*ReactorBackend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(module) == 0 || len(module) > MaxModuleBytes || opts.MemoryLimitPages > 65536 || opts.MaxStderrBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	if opts.MemoryLimitPages == 0 {
		opts.MemoryLimitPages = DefaultMemoryPages
	}
	if opts.MaxStderrBytes == 0 {
		opts.MaxStderrBytes = 64 << 10
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(opts.MemoryLimitPages).WithCloseOnContextDone(true))
	fail := func(err error) (*ReactorBackend, error) {
		_ = r.Close(context.Background())
		return nil, err
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return fail(err)
	}
	compiled, err := r.CompileModule(ctx, module)
	if err != nil {
		return fail(fmt.Errorf("compile WebAssembly reactor: %w", err))
	}
	exports := compiled.ExportedFunctions()
	if _, command := exports["_start"]; command {
		return fail(fmt.Errorf("%w: a module exporting _start is a command, not a reactor", plugin.ErrUnsupported))
	}
	for _, name := range []string{"ext_plugin_abi_version", "ext_plugin_open", "ext_plugin_call", "ext_plugin_close", "ext_plugin_alloc", "ext_plugin_free"} {
		if _, ok := exports[name]; !ok {
			return fail(fmt.Errorf("%w: reactor does not export %s", plugin.ErrUnsupported, name))
		}
	}
	config := wazero.NewModuleConfig().WithName("ext-plugin").WithStartFunctions().
		WithStdout(&limitedWriter{writer: opts.Stderr, remaining: opts.MaxStderrBytes}).
		WithStderr(&limitedWriter{writer: opts.Stderr, remaining: opts.MaxStderrBytes}).
		WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader)
	runCtx, cancel := context.WithCancel(context.Background())
	config = config.WithNanosleep(func(ns int64) {
		timer := time.NewTimer(time.Duration(ns))
		defer timer.Stop()
		select {
		case <-runCtx.Done():
		case <-timer.C:
		}
	})
	mod, err := r.InstantiateModule(runCtx, compiled, config)
	if err != nil {
		cancel()
		return fail(fmt.Errorf("instantiate WebAssembly reactor: %w", err))
	}
	b := &ReactorBackend{
		runtime: r, module: mod, cancel: cancel,
		call: mod.ExportedFunction("ext_plugin_call"), closeFn: mod.ExportedFunction("ext_plugin_close"),
		alloc: mod.ExportedFunction("ext_plugin_alloc"), free: mod.ExportedFunction("ext_plugin_free"),
		gate: make(chan struct{}, 1), closed: make(chan struct{}), done: make(chan struct{}),
	}
	abort := func(err error) (*ReactorBackend, error) {
		cancel()
		return fail(err)
	}
	if init := mod.ExportedFunction("_initialize"); init != nil {
		if _, err := init.Call(ctx); err != nil {
			return abort(fmt.Errorf("initialize WebAssembly reactor: %w", err))
		}
	}
	version, err := mod.ExportedFunction("ext_plugin_abi_version").Call(ctx)
	if err != nil || len(version) != 1 || uint32(version[0]) != reactorABIVersion {
		return abort(fmt.Errorf("%w: reactor ABI version is not %d", plugin.ErrMismatch, reactorABIVersion))
	}
	opened, err := mod.ExportedFunction("ext_plugin_open").Call(ctx)
	if err != nil || len(opened) != 1 || opened[0] == 0 {
		return abort(fmt.Errorf("%w: reactor could not open a session", plugin.ErrClosed))
	}
	b.handle = opened[0]
	// One response buffer of the maximum frame size, reused by every call, as the
	// C ABI requires; its pages commit only as the guest writes to them.
	if b.respPtr, err = b.allocate(ctx, plugin.MaxFrameBytes); err != nil {
		return abort(err)
	}
	if b.lenPtr, err = b.allocate(ctx, 4); err != nil {
		return abort(err)
	}
	return b, nil
}

func (b *ReactorBackend) allocate(ctx context.Context, size uint32) (uint32, error) {
	out, err := b.alloc.Call(ctx, uint64(size))
	if err != nil || len(out) != 1 || uint32(out[0]) == 0 {
		return 0, fmt.Errorf("%w: reactor allocation of %d bytes failed", plugin.ErrInvalid, size)
	}
	return uint32(out[0]), nil
}

// Handshake sends the absolute deadline and validates the descriptor.
func (b *ReactorBackend) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	var d plugin.Descriptor
	data, err := b.exchange(ctx, reactorHandshake, struct {
		Deadline time.Time `json:"deadline"`
	}{deadline})
	if err == nil {
		err = plugin.Decode(data, &d)
	}
	if err == nil {
		err = d.Validate()
	}
	if err != nil {
		_ = b.Close()
	}
	return d, err
}

// Invoke sends one request. The request deadline bounds the call.
func (b *ReactorBackend) Invoke(ctx context.Context, req plugin.Request) (plugin.Response, error) {
	if !req.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, req.Deadline)
		defer cancel()
	}
	var resp plugin.Response
	data, err := b.exchange(ctx, reactorInvoke, req)
	if err == nil {
		err = plugin.Decode(data, &resp)
	}
	if err == nil {
		err = resp.Validate(req.ID)
	}
	if err != nil {
		_ = b.Close()
	}
	return resp, err
}

func (b *ReactorBackend) exchange(ctx context.Context, op uint32, value any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
	request, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(request) == 0 || len(request) > plugin.MaxFrameBytes {
		return nil, plugin.ErrInvalid
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.closed:
		return nil, plugin.ErrClosed
	case b.gate <- struct{}{}:
	}
	defer func() { <-b.gate }()
	select {
	case <-b.closed:
		return nil, plugin.ErrClosed
	default:
	}
	// The module runs under ctx: if the deadline passes mid-call, wazero closes
	// the module, the call returns an error, and the backend is unusable.
	return b.roundTrip(ctx, op, request)
}

func (b *ReactorBackend) roundTrip(ctx context.Context, op uint32, request []byte) ([]byte, error) {
	size := uint32(len(request))
	ptr, err := b.allocate(ctx, size)
	if err != nil {
		_ = b.Close()
		return nil, err
	}
	memory := b.module.Memory()
	if !memory.Write(ptr, request) {
		_ = b.Close()
		return nil, plugin.ErrInvalid
	}
	out, err := b.call.Call(ctx, b.handle, uint64(op), uint64(ptr), uint64(size), uint64(b.respPtr), uint64(plugin.MaxFrameBytes), uint64(b.lenPtr))
	if err != nil {
		_ = b.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: %v", plugin.ErrClosed, err)
	}
	// The module may have grown memory; read through the live view.
	memory = b.module.Memory()
	if _, err := b.free.Call(ctx, uint64(ptr), uint64(size)); err != nil {
		_ = b.Close()
		return nil, err
	}
	switch uint32(out[0]) {
	case reactorOK:
		n, ok := memory.ReadUint32Le(b.lenPtr)
		if !ok || n > plugin.MaxFrameBytes {
			_ = b.Close()
			return nil, plugin.ErrInvalid
		}
		data, ok := memory.Read(b.respPtr, n)
		if !ok {
			_ = b.Close()
			return nil, plugin.ErrInvalid
		}
		return append([]byte(nil), data...), nil
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

// Close stops admission and releases the module once an outstanding call returns.
func (b *ReactorBackend) Close() error {
	b.once.Do(func() {
		close(b.closed)
		go func() {
			// Closing the context first stops a call that is still running.
			b.cancel()
			b.gate <- struct{}{}
			_, _ = b.closeFn.Call(context.Background(), b.handle)
			_ = b.runtime.Close(context.Background())
			<-b.gate
			close(b.done)
		}()
	})
	return nil
}

// WaitClosed waits for the module to be released after Close.
func (b *ReactorBackend) WaitClosed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return nil
	}
}

// ReactorProfile describes the reactor backend.
func ReactorProfile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "wasm/reactor", Protocols: []string{plugin.APIVersion}, Cancellation: "module", ProcessOwner: "wazero"}
}
