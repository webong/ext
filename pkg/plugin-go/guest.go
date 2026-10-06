//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

/*
#include "ctx_guest.h"
ctx_status ctx_go_guest_create(const uint8_t *,size_t,uint32_t,uintptr_t,ctx_guest **);
ctx_status ctx_go_guest_emit(ctx_guest_emit,void *,uint32_t,uint8_t *,size_t);
ctx_status ctx_go_guest_invoke(ctx_guest *,const uint8_t *,size_t,uint32_t,uintptr_t,ctx_buffer *);
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/author"
	"math"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

// Guest implements plugin.Endpoint through the shared C guest dispatcher.
// Destroy must follow connection shutdown; it waits for active calls. Handler
// code stays in Go and receives its original caller context and values.
type Guest struct {
	mu      sync.RWMutex
	ptr     *C.ctx_guest
	handler cgo.Handle
}

func NewGuest(d plugin.Descriptor, options plugin.GuestOptions) (*Guest, error) {
	if options.Handler == nil {
		return nil, plugin.ErrDenied
	}
	if options.MaxCallDuration < 0 || options.MaxCallDuration/time.Millisecond > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	g := &Guest{handler: cgo.NewHandle(options.Handler)}
	millis := options.MaxCallDuration / time.Millisecond
	if options.MaxCallDuration > 0 && millis == 0 {
		millis = 1
	}
	s := C.ctx_go_guest_create((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), C.uint32_t(millis), C.uintptr_t(g.handler), &g.ptr)
	if s != C.CTX_OK {
		g.handler.Delete()
		return nil, status(s)
	}
	return g, nil
}
func (g *Guest) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return plugin.Descriptor{}, err
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.ptr == nil {
		return plugin.Descriptor{}, plugin.ErrClosed
	}
	var out C.ctx_buffer
	s := C.ctx_guest_descriptor(g.ptr, &out)
	defer C.ctx_buffer_free(&out)
	if err := status(s); err != nil {
		return plugin.Descriptor{}, err
	}
	var d plugin.Descriptor
	err := plugin.Decode(C.GoBytes(unsafe.Pointer(out.data), C.int(out.len)), &d)
	return d, err
}

// invalidRequest reports an unusable request the way plugin.Guest does: as a
// public response error, not a failed call, so hosts see one behavior.
func invalidRequest(r plugin.Request) (plugin.Response, error) {
	response := plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Error: &plugin.RemoteError{Code: "invalid_request", Message: "request does not match selected contract"}}
	return response, response.Validate(r.ID)
}
func (g *Guest) Invoke(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	var result plugin.Response
	// Preserve Guest's public operation_failed response for an already canceled
	// valid invocation: the handler adapter observes the context before dispatch.
	data, err := json.Marshal(r)
	if err != nil {
		return invalidRequest(r)
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.ptr == nil {
		return result, plugin.ErrClosed
	}
	call := cgo.NewHandle(ctx)
	defer call.Delete()
	var out C.ctx_buffer
	s := C.ctx_go_guest_invoke(g.ptr, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), C.uint32_t(math.MaxUint32), C.uintptr_t(call), &out)
	defer C.ctx_buffer_free(&out)
	if err = status(s); err != nil {
		if errors.Is(err, plugin.ErrInvalid) {
			return invalidRequest(r)
		}
		return result, err
	}
	err = plugin.Decode(C.GoBytes(unsafe.Pointer(out.data), C.int(out.len)), &result)
	return result, err
}
func (g *Guest) Destroy() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ptr != nil {
		C.ctx_guest_destroy(g.ptr)
		g.ptr = nil
		g.handler.Delete()
	}
}

//export ctxGoGuestHandler
func ctxGoGuestHandler(handle, call C.uintptr_t, data *C.uint8_t, n C.size_t, millis C.uint32_t, emit C.ctx_guest_emit, sink unsafe.Pointer) (result C.int32_t) {
	result = C.CTX_IO
	defer func() {
		if recover() != nil {
			result = C.CTX_IO
		}
	}()
	handler := cgo.Handle(handle).Value().(plugin.Handler)
	parent := cgo.Handle(call).Value().(context.Context)
	ctx, cancel := context.WithTimeout(parent, time.Duration(millis)*time.Millisecond)
	defer cancel()
	var request plugin.Request
	if err := plugin.Decode(C.GoBytes(unsafe.Pointer(data), C.int(n)), &request); err != nil {
		return C.CTX_INVALID
	}
	// Go can honor the original nanosecond deadline within C's rounded budget.
	ctx, deadlineCancel := context.WithDeadline(ctx, request.Deadline)
	defer deadlineCancel()
	var payload json.RawMessage
	err := ctx.Err()
	if err == nil {
		payload, err = handler(ctx, request)
	}
	if err == nil {
		err = ctx.Err()
	}
	var kind C.uint32_t = C.CTX_GUEST_PAYLOAD
	if err != nil {
		var remote *plugin.RemoteError
		if !errors.As(err, &remote) || remote == nil {
			return backendStatus(err)
		}
		payload, err = json.Marshal(remote)
		if err != nil {
			return C.CTX_INVALID
		}
		kind = C.CTX_GUEST_PUBLIC_ERROR
	} else if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	return C.ctx_go_guest_emit(emit, sink, kind, (*C.uint8_t)(unsafe.Pointer(&payload[0])), C.size_t(len(payload)))
}

var _ plugin.Endpoint = (*Guest)(nil)

// GuestFromRegistry constructs an immutable C-dispatched guest from idiomatic
// Go typed handlers. The caller must Destroy it after all invocations finish.
func GuestFromRegistry(r *author.Registry, opts author.Options) (*Guest, error) {
	if r == nil {
		return nil, plugin.ErrInvalid
	}
	opts.SchemaEngine = sharedSchemas{}
	d, options, err := r.Snapshot(opts)
	if err != nil {
		return nil, err
	}
	return NewGuest(d, options)
}
