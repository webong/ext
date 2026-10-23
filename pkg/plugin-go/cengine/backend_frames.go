//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

/*
#include "ext_go_bridge.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unsafe"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// A backend that carries frames hands them to the engine, which validates and
// decides exactly as it does for a process it reads. This file is the Go side of
// that: the plugin.ProgressSink a call's context carries, and the
// plugin.ReverseHost a plugin.ReverseBackend is given, both ending in the
// engine's ext_host_backend_* entry points. Backends that cannot carry frames
// (HashiCorp, native Go, the C ABI, the WebAssembly reactor) never use either, as
// with the pure-Go session; a WebAssembly command is a JSON-line client and does.

// enter reserves the engine host for one call into it. Close waits for these, so
// the host is never destroyed under a frame; false once the backend is closed.
func (b *backendBinding) enter() (*C.ext_host, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.host == nil {
		return nil, false
	}
	b.inflight++
	return b.host, true
}

func (b *backendBinding) leave() {
	b.mu.Lock()
	b.inflight--
	if b.inflight == 0 {
		b.idle.Broadcast()
	}
	b.mu.Unlock()
}

// withFrames gives a call's context what the pure-Go session gives a backend: the
// sink for the events of an operation that declared progress, and the callers a
// plugin in the host's own process uses for reverse calls. Reverse handlers derive
// from the caller's context as the engine holds it, before these values.
func (b *backendBinding) withFrames(ctx context.Context, request plugin.Request) context.Context {
	if request.Progress != nil {
		ctx = plugin.WithProgressSink(ctx, engineSink{b: b, parent: request.ID})
	}
	b.mu.Lock()
	reverse := b.reverse
	b.mu.Unlock()
	if reverse {
		ctx = plugin.WithReverse(ctx, engineReverse{b: b, parent: request.ID})
		ctx = plugin.WithSessionReverse(ctx, engineReverse{b: b})
	}
	return ctx
}

// engineSink passes the events of one call to the engine, which checks, limits and
// delivers them.
type engineSink struct {
	b      *backendBinding
	parent string
}

func (s engineSink) Deliver(p plugin.Progress) (bool, error) {
	frame, err := json.Marshal(plugin.ProgressFrame{Frame: plugin.FrameProgress, APIVersion: plugin.APIVersion, Parent: s.parent, Progress: p})
	if err != nil || len(frame) > plugin.MaxFrameBytes {
		return false, plugin.ErrInvalid
	}
	host, ok := s.b.enter()
	if !ok {
		return false, plugin.ErrClosed
	}
	defer s.b.leave()
	var delivered C.int
	if err := status(C.ext_host_backend_progress(host, (*C.uint8_t)(unsafe.Pointer(&frame[0])), C.size_t(len(frame)), &delivered)); err != nil {
		return false, err
	}
	return delivered != 0, nil
}

var _ plugin.ProgressSink = engineSink{}

func refused(code string) *plugin.ReverseResult {
	return &plugin.ReverseResult{Error: &plugin.RemoteError{Code: code, Message: "reverse call refused"}}
}

// AdmitReverse implements plugin.ReverseHost for a backend that carries reverse
// frames. The engine decides: it refuses with a stable code, or admits, and run
// executes the host's hooks on whatever goroutine the backend chooses.
func (b *backendBinding) AdmitReverse(frame plugin.ReverseRequestFrame) (func() plugin.ReverseResult, *plugin.ReverseResult, error) {
	return b.admit(frame, nil)
}

var _ plugin.ReverseHost = (*backendBinding)(nil)

// admit is AdmitReverse for a request made in the host's process: caller, the
// plugin's own context, ends the handler early.
func (b *backendBinding) admit(frame plugin.ReverseRequestFrame, caller context.Context) (func() plugin.ReverseResult, *plugin.ReverseResult, error) {
	data, err := json.Marshal(frame)
	if err != nil || len(data) > plugin.MaxFrameBytes {
		return nil, nil, fmt.Errorf("%w: malformed reverse request", plugin.ErrProtocol)
	}
	host, ok := b.enter()
	if !ok {
		return nil, refused(plugin.ReverseUnavailable), nil
	}
	var ticket *C.ext_reverse_ticket
	var answer C.ext_buffer
	if err := status(C.ext_host_backend_reverse_admit(host, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), &ticket, &answer)); err != nil {
		b.leave()
		return nil, nil, err
	}
	if ticket == nil {
		defer b.leave()
		return nil, answerResult(&answer), nil
	}
	return func() plugin.ReverseResult {
		defer b.leave()
		var cancel *C.ext_cancel
		var watched chan struct{}
		stop := func() {}
		if caller != nil && C.ext_cancel_create(&cancel) == C.EXT_OK {
			watched = make(chan struct{})
			unwatch := context.AfterFunc(caller, func() { C.ext_cancel_signal(cancel); close(watched) })
			stop = func() {
				if !unwatch() {
					<-watched
				}
				C.ext_cancel_destroy(cancel)
			}
		}
		defer stop()
		var out C.ext_buffer
		if s := C.ext_host_backend_reverse_run(host, ticket, cancel, &out); s != C.EXT_OK {
			return *refused("operation_failed")
		}
		return *answerResult(&out)
	}, nil, nil
}

// answerResult reads the reverse_response frame the engine built and frees it.
func answerResult(answer *C.ext_buffer) *plugin.ReverseResult {
	defer C.ext_buffer_free(answer)
	var frame plugin.ReverseResponseFrame
	if answer.data == nil || plugin.Decode(C.GoBytes(unsafe.Pointer(answer.data), C.int(answer.len)), &frame) != nil {
		return &plugin.ReverseResult{Error: &plugin.RemoteError{Code: "operation_failed", Message: "host service failed"}}
	}
	return &plugin.ReverseResult{Payload: frame.Payload, Error: frame.Error}
}

// engineReverse lets a plugin in the host's own process call the host's reverse
// services directly, as the pure-Go session's reverse caller does. parent is the
// forward call it belongs to, empty for session scope.
type engineReverse struct {
	b      *backendBinding
	parent string
}

func (c engineReverse) Call(ctx context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.b.reverseTimeout())
	}
	frame := plugin.ReverseRequestFrame{Frame: plugin.FrameReverseRequest, APIVersion: plugin.APIVersion, ID: c.b.nextReverseID(), Parent: c.parent, Contract: ref, Operation: operation, Deadline: deadline, Payload: payload}
	if err := frame.Validate(); err != nil {
		return nil, plugin.ErrInvalid
	}
	run, immediate, err := c.b.admit(frame, ctx)
	if err != nil {
		return nil, err
	}
	var result plugin.ReverseResult
	if immediate != nil {
		result = *immediate
	} else {
		result = run()
	}
	if result.Error != nil {
		e := *result.Error
		return nil, &e
	}
	return result.Payload, nil
}

var _ plugin.ReverseCaller = engineReverse{}

func (b *backendBinding) nextReverseID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextReverse++
	return fmt.Sprintf("r%d", b.nextReverse)
}

func (b *backendBinding) reverseTimeout() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reverseOptions.Timeout > 0 {
		return b.reverseOptions.Timeout
	}
	return plugin.DefaultTimeout
}
