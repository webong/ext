package wasm

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// ReactorABI describes the exported functions of an application-defined
// request/response reactor. The web library hosts this mechanism without
// assigning meaning to operation or status values.
type ReactorABI struct {
	Version         uint32
	MaxMessageBytes uint32
	SuccessStatus   uint32
	VersionExport   string
	OpenExport      string
	CallExport      string
	CloseExport     string
	AllocateExport  string
	FreeExport      string
	// InitializeExport is called when present. DisallowedExport rejects a
	// module that exposes a mutually exclusive entry point such as _start.
	InitializeExport string
	DisallowedExport string
}

func (a ReactorABI) validate() error {
	if a.Version == 0 || a.MaxMessageBytes == 0 || a.MaxMessageBytes > MaxModuleBytes ||
		a.VersionExport == "" || a.OpenExport == "" || a.CallExport == "" ||
		a.CloseExport == "" || a.AllocateExport == "" || a.FreeExport == "" {
		return ErrInvalid
	}
	return nil
}

// ReactorOptions configures a reactor instance.
type ReactorOptions struct {
	// MemoryLimitPages bounds guest linear memory (64 KiB/page). Zero uses 4096.
	MemoryLimitPages uint32
	// Stderr receives guest diagnostics written to stdout or stderr; nil discards
	// them. A reactor has no stdin and nothing is inherited from the host.
	Stderr io.Writer
	// MaxStderrBytes caps forwarded diagnostics. Zero defaults to 64 KiB.
	MaxStderrBytes int64
}

// Reactor hosts one request/response WebAssembly reactor. Calls are serialized
// because a module instance is single-threaded.
type Reactor struct {
	runtime wazero.Runtime
	module  api.Module
	abi     ReactorABI
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

// OpenReactor compiles module, validates the configured ABI exports, and opens
// one reactor handle. ctx bounds compilation and startup, not the instance.
func OpenReactor(ctx context.Context, module []byte, abi ReactorABI, opts ReactorOptions) (*Reactor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := abi.validate(); err != nil {
		return nil, err
	}
	if len(module) == 0 || len(module) > MaxModuleBytes || opts.MemoryLimitPages > 65536 || opts.MaxStderrBytes < 0 {
		return nil, ErrInvalid
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
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(opts.MemoryLimitPages).WithCloseOnContextDone(true))
	fail := func(err error) (*Reactor, error) {
		_ = runtime.Close(context.Background())
		return nil, err
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return fail(err)
	}
	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		return fail(fmt.Errorf("compile WebAssembly reactor: %w", err))
	}
	exports := compiled.ExportedFunctions()
	if abi.DisallowedExport != "" {
		if _, exists := exports[abi.DisallowedExport]; exists {
			return fail(fmt.Errorf("%w: reactor exports disallowed function %s", ErrUnsupported, abi.DisallowedExport))
		}
	}
	for _, name := range []string{abi.VersionExport, abi.OpenExport, abi.CallExport, abi.CloseExport, abi.AllocateExport, abi.FreeExport} {
		if _, ok := exports[name]; !ok {
			return fail(fmt.Errorf("%w: reactor does not export %s", ErrUnsupported, name))
		}
	}
	config := wazero.NewModuleConfig().WithName("wasm-reactor").WithStartFunctions().
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
	moduleInstance, err := runtime.InstantiateModule(runCtx, compiled, config)
	if err != nil {
		cancel()
		return fail(fmt.Errorf("instantiate WebAssembly reactor: %w", err))
	}
	reactor := &Reactor{
		runtime: runtime, module: moduleInstance, abi: abi, cancel: cancel,
		call: moduleInstance.ExportedFunction(abi.CallExport), closeFn: moduleInstance.ExportedFunction(abi.CloseExport),
		alloc: moduleInstance.ExportedFunction(abi.AllocateExport), free: moduleInstance.ExportedFunction(abi.FreeExport),
		gate: make(chan struct{}, 1), closed: make(chan struct{}), done: make(chan struct{}),
	}
	abort := func(err error) (*Reactor, error) {
		cancel()
		return fail(err)
	}
	if abi.InitializeExport != "" {
		if initialize := moduleInstance.ExportedFunction(abi.InitializeExport); initialize != nil {
			if _, err := initialize.Call(ctx); err != nil {
				return abort(fmt.Errorf("initialize WebAssembly reactor: %w", err))
			}
		}
	}
	version, err := moduleInstance.ExportedFunction(abi.VersionExport).Call(ctx)
	if err != nil || len(version) != 1 || uint32(version[0]) != abi.Version {
		return abort(fmt.Errorf("%w: reactor ABI version is not %d", ErrMismatch, abi.Version))
	}
	opened, err := moduleInstance.ExportedFunction(abi.OpenExport).Call(ctx)
	if err != nil || len(opened) != 1 || opened[0] == 0 {
		return abort(fmt.Errorf("%w: reactor could not open a session", ErrClosed))
	}
	reactor.handle = opened[0]
	if reactor.respPtr, err = reactor.allocate(ctx, abi.MaxMessageBytes); err != nil {
		return abort(err)
	}
	if reactor.lenPtr, err = reactor.allocate(ctx, 4); err != nil {
		return abort(err)
	}
	return reactor, nil
}

func (r *Reactor) allocate(ctx context.Context, size uint32) (uint32, error) {
	out, err := r.alloc.Call(ctx, uint64(size))
	if err != nil || len(out) != 1 || uint32(out[0]) == 0 {
		return 0, fmt.Errorf("%w: reactor allocation of %d bytes failed", ErrInvalid, size)
	}
	return uint32(out[0]), nil
}

// Call invokes one application-defined operation and returns its raw status and
// response bytes. The caller owns all protocol encoding and status semantics.
func (r *Reactor) Call(ctx context.Context, operation uint32, request []byte) (uint32, []byte, error) {
	if len(request) == 0 || uint32(len(request)) > r.abi.MaxMessageBytes {
		return 0, nil, ErrInvalid
	}
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-r.closed:
		return 0, nil, ErrClosed
	case r.gate <- struct{}{}:
	}
	defer func() { <-r.gate }()
	select {
	case <-r.closed:
		return 0, nil, ErrClosed
	default:
	}
	return r.callRaw(ctx, operation, request)
}

func (r *Reactor) callRaw(ctx context.Context, operation uint32, request []byte) (uint32, []byte, error) {
	size := uint32(len(request))
	ptr, err := r.allocate(ctx, size)
	if err != nil {
		_ = r.Close()
		return 0, nil, err
	}
	memory := r.module.Memory()
	if memory == nil || !memory.Write(ptr, request) {
		_ = r.Close()
		return 0, nil, ErrInvalid
	}
	out, err := r.call.Call(ctx, r.handle, uint64(operation), uint64(ptr), uint64(size), uint64(r.respPtr), uint64(r.abi.MaxMessageBytes), uint64(r.lenPtr))
	if err != nil {
		_ = r.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		return 0, nil, fmt.Errorf("%w: %v", ErrClosed, err)
	}
	memory = r.module.Memory()
	if _, err := r.free.Call(ctx, uint64(ptr), uint64(size)); err != nil {
		_ = r.Close()
		return 0, nil, err
	}
	if len(out) != 1 || memory == nil {
		_ = r.Close()
		return 0, nil, ErrInvalid
	}
	status := uint32(out[0])
	if status != r.abi.SuccessStatus {
		return status, nil, nil
	}
	length, ok := memory.ReadUint32Le(r.lenPtr)
	if !ok || length > r.abi.MaxMessageBytes {
		_ = r.Close()
		return 0, nil, ErrInvalid
	}
	data, ok := memory.Read(r.respPtr, length)
	if !ok {
		_ = r.Close()
		return 0, nil, ErrInvalid
	}
	return status, append([]byte(nil), data...), nil
}

// Close stops admission and releases the module once an outstanding call exits.
func (r *Reactor) Close() error {
	r.once.Do(func() {
		close(r.closed)
		go func() {
			r.cancel()
			r.gate <- struct{}{}
			_, _ = r.closeFn.Call(context.Background(), r.handle)
			_ = r.runtime.Close(context.Background())
			<-r.gate
			close(r.done)
		}()
	})
	return nil
}

// WaitClosed waits for resources to be released after Close.
func (r *Reactor) WaitClosed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return nil
	}
}
