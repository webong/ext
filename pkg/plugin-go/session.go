//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

/*
#include "ctx_host.h"
ctx_status ctx_go_set_hooks(ctx_host *,uintptr_t);
*/
import "C"
import (
	"context"
	"encoding/json"
	"github.com/webong/ctx/pkg/plugin"
	"math"
	"runtime/cgo"
	"time"
	"unsafe"
)

// Session is the Go session facade over the shared C engine. It implements the
// author.Caller interface and uses existing runtime Backend factories. Close
// drains and frees the C allocation; Abort stops work and then frees it.
type Session struct{ *Host }

func Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (*Session, error) {
	if options.Verify == nil || options.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if options.Connect == nil || options.Timeout < 0 || options.Timeout/time.Millisecond > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	requirements := make([]map[string]any, 0, len(options.Requirements))
	for _, r := range options.Requirements {
		value := map[string]any{"contract": r.Contract, "operation": r.Operation}
		if r.Identity != nil {
			value["identity"] = r.Identity
		}
		requirements = append(requirements, value)
	}
	data, err := json.Marshal(map[string]any{"descriptor": d, "requirements": requirements})
	if err != nil {
		return nil, err
	}
	if _, err = EngineCall("descriptor.requirements", data); err != nil {
		return nil, err
	}
	h, err := NewWithBackend(d, BackendOptions{Connect: options.Connect, Concurrent: true}, func([]byte) error { return nil }, func([]byte) error { return nil })
	if err != nil {
		return nil, err
	}
	h.callTimeout = options.Timeout
	policy := h.policy.Value().(*policies)
	policy.verifyContext = func(ctx context.Context, raw []byte) error {
		var d plugin.Descriptor
		if err := plugin.Decode(raw, &d); err != nil {
			return err
		}
		return options.Verify(ctx, d)
	}
	policy.authorizeContext = func(ctx context.Context, raw []byte) error {
		var r plugin.Request
		if err := plugin.Decode(raw, &r); err != nil {
			return err
		}
		return options.Authorize(ctx, r)
	}
	policy.observer = options.Observer
	if err = status(C.ctx_go_set_hooks(h.ptr, C.uintptr_t(h.policy))); err != nil {
		h.Destroy()
		return nil, err
	}
	if err = h.Start(ctx); err != nil {
		h.Destroy()
		return nil, err
	}
	return &Session{h}, nil
}
func (s *Session) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.State() == plugin.StateClosed {
		return s.Host.Close()
	}
	if err := s.DrainContext(ctx); err != nil {
		return err
	}
	err := s.Host.Close()
	s.Destroy()
	return err
}
func (s *Session) Abort() error { err := s.Host.Close(); s.Destroy(); return err }

//export ctxGoContextPolicy
func ctxGoContextPolicy(handle C.uintptr_t, kind C.int, caller C.uintptr_t, data *C.uint8_t, n C.size_t) (result C.int32_t) {
	result = 1
	defer func() {
		if recover() != nil {
			result = 1
		}
	}()
	p := cgo.Handle(handle).Value().(*policies)
	ctx := context.Background()
	if caller != 0 {
		ctx = cgo.Handle(caller).Value().(context.Context)
	}
	if ctx.Err() != nil {
		return 1
	}
	f := p.authorizeContext
	if kind == 0 {
		f = p.verifyContext
	}
	if f == nil {
		return ctxGoPolicy(handle, kind, data, n)
	}
	if recordCallbackError(ctx, f(ctx, C.GoBytes(unsafe.Pointer(data), C.int(n)))) == nil {
		return 0
	}
	return 1
}

//export ctxGoObserver
func ctxGoObserver(handle C.uintptr_t, caller C.uintptr_t, data *C.uint8_t, n C.size_t) {
	defer func() { _ = recover() }()
	p := cgo.Handle(handle).Value().(*policies)
	if p.observer == nil {
		return
	}
	var event struct {
		Stage     string             `json:"stage"`
		Identity  plugin.Identity    `json:"identity"`
		Contract  plugin.ContractRef `json:"contract"`
		Operation string             `json:"operation"`
		RequestID string             `json:"requestID"`
		Code      string             `json:"code"`
		Duration  int64              `json:"durationMilliseconds"`
	}
	if plugin.Decode(C.GoBytes(unsafe.Pointer(data), C.int(n)), &event) != nil {
		return
	}
	ctx := context.Background()
	if caller != 0 {
		ctx = cgo.Handle(caller).Value().(context.Context)
	}
	p.observer(ctx, plugin.Event{Stage: event.Stage, Identity: event.Identity, Contract: event.Contract, Operation: event.Operation, RequestID: event.RequestID, Code: event.Code, Duration: time.Duration(event.Duration) * time.Millisecond})
}
