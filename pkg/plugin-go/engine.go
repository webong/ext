//go:build ext_cengine && cgo && (darwin || linux)

// Package goengine (github.com/webong/ext/pkg/plugin-go) binds Go to the C host engine.
// Build with -tags ext_cengine and link libext_host_static. Add the
// ext_cengine_shared tag to link the optional libext_host shared library.
package goengine

/*
#cgo CFLAGS: -I${SRCDIR}/../../pkg/plugin-engine/include
#cgo !ext_cengine_shared CFLAGS: -DEXT_HOST_STATIC
#cgo !ext_cengine_shared LDFLAGS: -lext_host_static -lpthread -lm
#cgo ext_cengine_shared LDFLAGS: -lext_host
#include "ext_host.h"
#include <stdlib.h>
void ext_go_call_init(ext_call_options *,uint32_t,const ext_cancel *,uintptr_t);
ext_status ext_go_create(const char *, const unsigned char *, size_t, uintptr_t, ext_host **);
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime/cgo"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/webong/ext/pkg/plugin"
)

type Policy func([]byte) error
type policies struct {
	verify, authorize               Policy
	verifyContext, authorizeContext func(context.Context, []byte) error
	observer                        plugin.Observer
}

// Host owns one C session. Close cancels; Destroy waits for active calls and
// releases memory. Policy callbacks must not reenter the host.
type Host struct {
	mu          sync.RWMutex
	ptr         *C.ext_host
	policy      cgo.Handle
	descriptor  plugin.Descriptor
	callTimeout time.Duration
	backend     *backendBinding
}

func New(path string, d plugin.Descriptor, verify, authorize Policy) (*Host, error) {
	if verify == nil || authorize == nil {
		return nil, plugin.ErrInvalid
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	// C strings cannot represent embedded NULs.
	for _, c := range path {
		if c == 0 {
			return nil, plugin.ErrInvalid
		}
	}
	h := &Host{policy: cgo.NewHandle(&policies{verify: verify, authorize: authorize}), descriptor: d.Clone()}
	s := C.ext_go_create(p, (*C.uchar)(unsafe.Pointer(&data[0])), C.size_t(len(data)), C.uintptr_t(h.policy), &h.ptr)
	if s != 0 {
		h.policy.Delete()
		return nil, status(s)
	}
	return h, nil
}
func status(s C.ext_status) error {
	switch s {
	case C.EXT_OK:
		return nil
	case C.EXT_INVALID:
		return plugin.ErrInvalid
	case C.EXT_DENIED:
		return plugin.ErrDenied
	case C.EXT_MISMATCH:
		return plugin.ErrMismatch
	case C.EXT_UNSUPPORTED:
		return plugin.ErrUnsupported
	case C.EXT_NOT_FOUND:
		return plugin.ErrNotFound
	case C.EXT_AMBIGUOUS:
		return plugin.ErrAmbiguous
	case C.EXT_DRAINING:
		return plugin.ErrDraining
	case C.EXT_CLOSED:
		return plugin.ErrClosed
	case C.EXT_CANCELED:
		return context.Canceled
	case C.EXT_TIMEOUT:
		return context.DeadlineExceeded
	}
	return fmt.Errorf("C host: %s", C.GoString(C.ext_host_status_string(s)))
}
func timeout(ctx context.Context) C.uint32_t {
	d := 30 * time.Second
	if until, ok := ctx.Deadline(); ok {
		d = time.Until(until)
	}
	if d < time.Millisecond {
		return 1
	}
	if d/time.Millisecond > math.MaxUint32 {
		return C.uint32_t(math.MaxUint32)
	}
	return C.uint32_t(d / time.Millisecond)
}

// Each invocation owns its cancellation signal and original Go context handle.
// Joining the signal callback before freeing prevents C pointer lifetime races.
type callScope struct {
	ctx     context.Context
	options C.ext_call_options
	cancel  *C.ext_cancel
	value   cgo.Handle
	finish  func()
}

func (h *Host) scope(ctx context.Context) (*callScope, error) {
	h.mu.RLock()
	if h.ptr == nil {
		h.mu.RUnlock()
		return nil, plugin.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		h.mu.RUnlock()
		return nil, err
	}
	duration := h.callTimeout
	if duration == 0 {
		duration = plugin.DefaultTimeout
	}
	ctx, cancelContext := context.WithTimeout(ctx, duration)
	ctx = errorContext(ctx)
	scope := &callScope{ctx: ctx}
	if err := status(C.ext_cancel_create(&scope.cancel)); err != nil {
		cancelContext()
		h.mu.RUnlock()
		return nil, err
	}
	scope.value = cgo.NewHandle(ctx)
	C.ext_go_call_init(&scope.options, timeout(ctx), scope.cancel, C.uintptr_t(scope.value))
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { C.ext_cancel_signal(scope.cancel); close(done) })
	scope.finish = func() {
		if !stop() {
			<-done
		}
		C.ext_cancel_destroy(scope.cancel)
		scope.value.Delete()
		cancelContext()
		h.mu.RUnlock()
	}
	return scope, nil
}
func (h *Host) Start(ctx context.Context) error {
	scope, err := h.scope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	err = scope.result(status(C.ext_host_start_with_options(h.ptr, &scope.options)))
	if scope.ctx.Err() != nil {
		return scope.ctx.Err()
	}
	return err
}
func (h *Host) CallRaw(ctx context.Context, request []byte) ([]byte, error) {
	if len(request) == 0 {
		return nil, plugin.ErrInvalid
	}
	scope, err := h.scope(ctx)
	if err != nil {
		return nil, err
	}
	defer scope.finish()
	var out C.ext_buffer
	s := C.ext_host_invoke_with_options(h.ptr, (*C.uint8_t)(unsafe.Pointer(&request[0])), C.size_t(len(request)), &scope.options, &out)
	defer C.ext_buffer_free(&out)
	if scope.ctx.Err() != nil {
		return nil, scope.ctx.Err()
	}
	if err := scope.result(status(s)); err != nil {
		return nil, err
	}
	return C.GoBytes(unsafe.Pointer(out.data), C.int(out.len)), nil
}
func (h *Host) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	if err := h.Start(ctx); err != nil {
		return plugin.Descriptor{}, err
	}
	return h.descriptor.Clone(), nil
}
func (h *Host) Invoke(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return plugin.Response{}, err
	}
	b, err = h.CallRaw(ctx, b)
	var resp plugin.Response
	if err == nil {
		err = plugin.Decode(b, &resp)
	}
	return resp, err
}
func (h *Host) Close() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.ptr != nil {
		C.ext_host_close(h.ptr)
	}
	if h.backend != nil {
		<-h.backend.closeDone
		h.backend.mu.Lock()
		defer h.backend.mu.Unlock()
		return h.backend.closeErr
	}
	return nil
}
func (h *Host) Destroy() {
	_ = h.Close()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ptr != nil {
		C.ext_host_destroy(h.ptr)
		h.ptr = nil
		h.policy.Delete()
	}
}
func ValidateJSON(data []byte) error {
	if len(data) == 0 {
		return plugin.ErrInvalid
	}
	return status(C.ext_host_validate_json((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))))
}

//export ctxGoPolicy
func ctxGoPolicy(handle C.uintptr_t, kind C.int, data *C.uint8_t, n C.size_t) (result C.int32_t) {
	result = 1
	defer func() {
		if recover() != nil {
			result = 1
		}
	}()
	p := cgo.Handle(handle).Value().(*policies)
	f := p.authorize
	if kind == 0 {
		f = p.verify
	}
	if f(C.GoBytes(unsafe.Pointer(data), C.int(n))) == nil {
		return 0
	}
	return 1
}

var _ plugin.Backend = (*Host)(nil)

// EngineCall runs a pure shared-engine service. Input/output are bounded JSON;
// no plugin is executed and no policy is inferred. See pkg/plugin-engine/services.md.
func EngineCall(operation string, input json.RawMessage) (json.RawMessage, error) {
	if len(input) == 0 || strings.IndexByte(operation, 0) >= 0 {
		return nil, plugin.ErrInvalid
	}
	name := C.CString(operation)
	defer C.free(unsafe.Pointer(name))
	var out C.ext_buffer
	s := C.ext_engine_call(name, (*C.uint8_t)(unsafe.Pointer(&input[0])), C.size_t(len(input)), &out)
	defer C.ext_buffer_free(&out)
	if err := status(s); err != nil {
		return nil, err
	}
	return C.GoBytes(unsafe.Pointer(out.data), C.int(out.len)), nil
}

// Descriptor returns a detached copy of the immutable selection.
func (h *Host) Descriptor() plugin.Descriptor { return h.descriptor.Clone() }

// Call implements author.Caller using C-generated identity, surface, deadlines
// and correlation IDs. Public domain errors retain their structured details.
func (h *Host) Call(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	data, err := json.Marshal(struct {
		Contract  plugin.ContractRef `json:"contract"`
		Operation string             `json:"operation"`
		Payload   json.RawMessage    `json:"payload,omitempty"`
	}{contract, operation, payload})
	if err != nil {
		return nil, err
	}
	scope, err := h.scope(ctx)
	if err != nil {
		return nil, err
	}
	defer scope.finish()
	var out C.ext_buffer
	s := C.ext_host_call(h.ptr, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), &scope.options, &out)
	defer C.ext_buffer_free(&out)
	if scope.ctx.Err() != nil {
		return nil, scope.ctx.Err()
	}
	if err = scope.result(status(s)); err != nil {
		return nil, err
	}
	var response plugin.Response
	if err = plugin.Decode(C.GoBytes(unsafe.Pointer(out.data), C.int(out.len)), &response); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, response.Error
	}
	return response.Payload, nil
}
