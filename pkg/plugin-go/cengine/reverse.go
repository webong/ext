//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

/*
#include "ext_go_bridge.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"runtime/cgo"
	"time"
	"unsafe"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// reverseBinding is what the C engine's reverse hooks call back into. The engine
// decides whether a reverse call is admitted (the declaration, the bounds, the
// plugin's deadline) and how an answer reaches the plugin; the binding supplies
// only the host's policy, Authorize and Handle, which are Go values.
type reverseBinding struct{ options plugin.ReverseOptions }

// validateReverse checks ReverseOptions as plugin.Open does (reverse.go in the
// contract module): both hooks are required, and the bounds are in range.
func validateReverse(o *plugin.ReverseOptions) error {
	if o.Handle == nil || o.Authorize == nil {
		return plugin.ErrDenied
	}
	if o.Timeout < 0 || o.MaxPayloadBytes < 0 || o.MaxPayloadBytes > plugin.MaxFrameBytes || o.MaxInFlight < 0 || o.MaxInFlight > 1024 {
		return plugin.ErrInvalid
	}
	return nil
}

// setReverse offers the services to the plugin. It must run before the host
// starts. The engine serves one reverse call at a time on the thread that is
// making the call, so MaxInFlight is accepted but not reached.
func (h *Host) setReverse(o *plugin.ReverseOptions) error {
	if err := validateReverse(o); err != nil {
		return err
	}
	timeout := uint64(0)
	if o.Timeout > 0 {
		timeout = uint64((o.Timeout + time.Millisecond - 1) / time.Millisecond)
		if timeout > math.MaxUint32 {
			timeout = math.MaxUint32
		}
	}
	binding := cgo.NewHandle(&reverseBinding{options: *o})
	if err := status(C.ext_go_set_services(h.ptr, C.uintptr_t(binding), C.uint32_t(timeout), C.uint32_t(o.MaxPayloadBytes), C.uint32_t(o.MaxInFlight))); err != nil {
		binding.Delete()
		return err
	}
	h.reverse = binding
	return nil
}

// request reads the Request the engine built for the plugin's frame, and the
// context the handler runs in. That context derives from the context of the call
// in flight, so its values (a host that is itself a plugin finds its upstream
// caller through plugin.ReverseFrom there) and its cancellation carry through;
// its deadline is the engine's, the earliest of the plugin's, the call's and the
// services' own bound.
func (b *reverseBinding) request(caller C.uintptr_t, timeout C.uint32_t, data *C.uint8_t, n C.size_t) (context.Context, context.CancelFunc, plugin.Request, error) {
	parent := context.Background()
	if caller != 0 {
		parent = cgo.Handle(caller).Value().(context.Context)
	}
	var r plugin.Request
	if err := plugin.Decode(C.GoBytes(unsafe.Pointer(data), C.int(n)), &r); err != nil {
		return nil, nil, r, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	return ctx, cancel, r, nil
}

//export ctxGoReverseAuthorize
func ctxGoReverseAuthorize(handle C.uintptr_t, caller C.uintptr_t, timeout C.uint32_t, data *C.uint8_t, n C.size_t) (result C.int32_t) {
	result = 1 // a policy that fails denies
	defer func() {
		if recover() != nil {
			result = 1
		}
	}()
	b := cgo.Handle(handle).Value().(*reverseBinding)
	ctx, cancel, r, err := b.request(caller, timeout, data, n)
	if err != nil {
		return 1
	}
	defer cancel()
	if b.options.Authorize(ctx, r.Clone()) != nil {
		return 1
	}
	return 0
}

//export ctxGoReverseHandle
func ctxGoReverseHandle(handle C.uintptr_t, caller C.uintptr_t, timeout C.uint32_t, data *C.uint8_t, n C.size_t, emit C.ext_reverse_emit, sink unsafe.Pointer) (result C.int32_t) {
	result = C.EXT_IO // a handler that fails, or panics, is a failure the plugin sees as operation_failed
	defer func() {
		if recover() != nil {
			result = C.EXT_IO
		}
	}()
	b := cgo.Handle(handle).Value().(*reverseBinding)
	ctx, cancel, r, err := b.request(caller, timeout, data, n)
	if err != nil {
		return result
	}
	defer cancel()
	payload, err := b.options.Handle(ctx, r.Clone())
	if err != nil {
		// A handler's own RemoteError is handed to the engine, which passes it on
		// if a peer would accept it and answers operation_failed otherwise.
		var remote *plugin.RemoteError
		if errors.As(err, &remote) && remote != nil {
			if raw, merr := json.Marshal(remote); merr == nil {
				return C.int32_t(C.ext_go_reverse_emit(emit, sink, C.uint32_t(C.EXT_HOST_REVERSE_PUBLIC_ERROR), bytesPointer(raw), C.size_t(len(raw))))
			}
		}
		return result
	}
	return C.int32_t(C.ext_go_reverse_emit(emit, sink, C.uint32_t(C.EXT_HOST_REVERSE_PAYLOAD), bytesPointer(payload), C.size_t(len(payload))))
}
