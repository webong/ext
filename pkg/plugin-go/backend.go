//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

/*
#include "ctx_host.h"
ctx_status ctx_go_create_backend(const uint8_t *, size_t, uintptr_t, uintptr_t, uint32_t, ctx_host **);
ctx_status ctx_go_emit(ctx_emit, void *, uint8_t *, size_t);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"

	"github.com/webong/ext/pkg/plugin"
)

// BackendOptions supplies runtime mechanics to the C engine. Connect must honor
// its context, and return a dedicated backend whose ownership transfers to the
// host. The C engine invokes it only after verification. Concurrent declares
// whether C may dispatch calls concurrently; false serializes admission.
// Callback contexts preserve the Go caller context and values, bounded by
// engine deadlines and backend lifetime. No callback may reenter the same Host.
type BackendOptions struct {
	Connect    func(context.Context, plugin.Descriptor) (plugin.Backend, error)
	Concurrent bool
}

type backendBinding struct {
	mu         sync.Mutex
	options    BackendOptions
	descriptor plugin.Descriptor
	ctx        context.Context
	cancel     context.CancelFunc
	backend    plugin.Backend
	closed     bool
	closeDone  chan struct{}
	closeErr   error
}

// NewWithBackend embeds the common C session engine around any Go Backend,
// including HashiCorp, WASI, native Go, C ABI, in-process and JSON-line bindings.
// It does not nest a Go Session. Backend code remains in its native runtime.
func NewWithBackend(d plugin.Descriptor, opts BackendOptions, verify, authorize Policy) (*Host, error) {
	if opts.Connect == nil || verify == nil || authorize == nil {
		return nil, plugin.ErrInvalid
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	binding := &backendBinding{options: opts, descriptor: d.Clone(), ctx: ctx, cancel: cancel, closeDone: make(chan struct{})}
	runtime := cgo.NewHandle(binding)
	h := &Host{policy: cgo.NewHandle(&policies{verify: verify, authorize: authorize}), descriptor: d.Clone(), backend: binding}
	var flags C.uint32_t
	if opts.Concurrent {
		flags = C.CTX_BACKEND_CONCURRENT
	}
	s := C.ctx_go_create_backend((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), C.uintptr_t(h.policy), C.uintptr_t(runtime), flags, &h.ptr)
	if s != C.CTX_OK {
		cancel()
		runtime.Delete()
		h.policy.Delete()
		return nil, status(s)
	}
	return h, nil
}

func backendStatus(err error) C.int32_t {
	switch {
	case err == nil:
		return C.CTX_OK
	case errors.Is(err, context.DeadlineExceeded):
		return C.CTX_TIMEOUT
	case errors.Is(err, context.Canceled):
		return C.CTX_CANCELED
	case errors.Is(err, plugin.ErrClosed):
		return C.CTX_CLOSED
	case errors.Is(err, plugin.ErrInvalid):
		return C.CTX_INVALID
	case errors.Is(err, plugin.ErrMismatch):
		return C.CTX_MISMATCH
	case errors.Is(err, plugin.ErrDenied):
		return C.CTX_DENIED
	case errors.Is(err, plugin.ErrUnsupported):
		return C.CTX_UNSUPPORTED
	case errors.Is(err, plugin.ErrDraining):
		return C.CTX_DRAINING
	default:
		return C.CTX_IO
	}
}
func emitJSON(value any, emit C.ctx_emit, sink unsafe.Pointer) C.int32_t {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || len(data) > plugin.MaxFrameBytes {
		return C.CTX_INVALID
	}
	return C.ctx_go_emit(emit, sink, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)))
}

func (b *backendBinding) bound(caller C.uintptr_t, duration time.Duration) (context.Context, func()) {
	parent := b.ctx
	if caller != 0 {
		parent = cgo.Handle(caller).Value().(context.Context)
	}
	ctx, cancel := context.WithTimeout(parent, duration)
	stop := context.AfterFunc(b.ctx, cancel)
	if b.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

//export ctxGoConnect
func ctxGoConnect(handle C.uintptr_t, timeout C.uint32_t, caller C.uintptr_t, emit C.ctx_emit, sink unsafe.Pointer) (result C.int32_t) {
	result = C.CTX_IO
	defer func() {
		if recover() != nil {
			result = C.CTX_IO
		}
	}()
	b := cgo.Handle(handle).Value().(*backendBinding)
	ctx, cancel := b.bound(caller, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	backend, err := b.options.Connect(ctx, b.descriptor.Clone())
	if err != nil {
		if backend != nil {
			_ = backend.Close()
		}
		return backendStatus(recordCallbackError(ctx, err))
	}
	if backend == nil {
		return C.CTX_INVALID
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = backend.Close()
		return C.CTX_CLOSED
	}
	b.backend = backend
	b.mu.Unlock()
	actual, err := backend.Handshake(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	return emitJSON(actual, emit, sink)
}

//export ctxGoInvoke
func ctxGoInvoke(handle C.uintptr_t, data *C.uint8_t, n C.size_t, timeout C.uint32_t, caller C.uintptr_t, emit C.ctx_emit, sink unsafe.Pointer) (result C.int32_t) {
	result = C.CTX_IO
	defer func() {
		if recover() != nil {
			result = C.CTX_IO
		}
	}()
	b := cgo.Handle(handle).Value().(*backendBinding)
	var request plugin.Request
	if n > plugin.MaxFrameBytes {
		return C.CTX_INVALID
	}
	if err := plugin.Decode(C.GoBytes(unsafe.Pointer(data), C.int(n)), &request); err != nil {
		return C.CTX_INVALID
	}
	ctx, cancel := b.bound(caller, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	ctx, deadlineCancel := context.WithDeadline(ctx, request.Deadline)
	defer deadlineCancel()
	if err := ctx.Err(); err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	b.mu.Lock()
	backend, isClosed := b.backend, b.closed
	b.mu.Unlock()
	if backend == nil || isClosed {
		return C.CTX_CLOSED
	}
	response, err := backend.Invoke(ctx, request)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	return emitJSON(response, emit, sink)
}

//export ctxGoCloseBackend
func ctxGoCloseBackend(handle C.uintptr_t) {
	b := cgo.Handle(handle).Value().(*backendBinding)
	defer close(b.closeDone)
	defer func() {
		if recover() != nil {
			b.mu.Lock()
			b.closeErr = errors.New("backend close failed")
			b.mu.Unlock()
		}
	}()
	b.cancel()
	b.mu.Lock()
	b.closed = true
	backend := b.backend
	b.mu.Unlock()
	if backend != nil {
		err := backend.Close()
		b.mu.Lock()
		b.closeErr = err
		b.mu.Unlock()
	}
}

//export ctxGoReleaseBackend
func ctxGoReleaseBackend(handle C.uintptr_t) {
	cgo.Handle(handle).Delete()
}

// Drain stops admission until admitted work finishes. Timeout reopens admission
// without aborting the backend. Close can abort concurrently. Duration is bounded
// to 1ms..UINT32_MAX milliseconds; this low-level call does not take a Go cancellation context.
func (h *Host) Drain(duration time.Duration) error {
	if duration < time.Millisecond || duration/time.Millisecond > math.MaxUint32 {
		return plugin.ErrInvalid
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.ptr == nil {
		return plugin.ErrClosed
	}
	return status(C.ctx_host_drain(h.ptr, C.uint32_t(duration/time.Millisecond)))
}

// State reports C-owned lifecycle state. Before Start it returns "created".
func (h *Host) State() plugin.State {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.ptr == nil {
		return plugin.StateClosed
	}
	switch C.ctx_host_get_state(h.ptr) {
	case C.CTX_HOST_READY:
		return plugin.StateReady
	case C.CTX_HOST_DRAINING:
		return plugin.StateDraining
	case C.CTX_HOST_FAILED:
		return plugin.StateFailed
	case C.CTX_HOST_CREATED:
		return plugin.State("created")
	default:
		return plugin.StateClosed
	}
}

// DrainContext rejects new work and drains admitted calls. Cancellation reopens
// admission; Close is the separate abort operation.
func (h *Host) DrainContext(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.ptr == nil {
		return plugin.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scope, err := resourceScope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	err = status(C.ctx_host_drain_with_options(h.ptr, &scope.options))
	if scope.ctx.Err() != nil {
		return scope.ctx.Err()
	}
	return err
}
