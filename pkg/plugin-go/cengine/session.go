//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

/*
#include "ext_go_bridge.h"
*/
import "C"
import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"math"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

// Session is the Go session facade over the shared C engine. It implements the
// author.Caller interface and uses existing runtime Backend factories. Close
// drains and frees the C allocation; Abort stops work and then frees it.
type Session struct {
	*Host
	done     chan struct{}
	doneOnce sync.Once
}

// Done is closed once the session has been closed or aborted.
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) finish() { s.doneOnce.Do(func() { close(s.done) }) }

// CallProgress is Call for an operation that declares progress: onProgress
// receives its events while the call runs, before CallProgress returns. The C
// engine validates each event and applies the rate limit, as the pure-Go
// session does: events beyond it are dropped and counted, the next event
// carries Dropped, and a last event with the remaining count arrives before the
// call returns. For an operation that did not declare progress, onProgress is
// never called.
func (s *Session) CallProgress(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage, onProgress func(plugin.Progress), options ...plugin.CallOptions) (json.RawMessage, error) {
	var o plugin.CallOptions
	if len(options) > 0 {
		o = options[0]
	}
	if err := o.Progress.Validate(); err != nil {
		return nil, err
	}
	return s.Host.callProgress(ctx, contract, operation, payload, onProgress, o.Progress)
}

// refuseReverse rejects Options.Reverse on a session whose backend the engine
// does not own. An extension backend (Open with Connect: in-process, HashiCorp,
// WASI, native Go) hands the engine one response at a time and carries no
// reverse frames, so the host is refused at open, never silently without the
// services it configured. OpenProcess serves them.
func refuseReverse(options plugin.Options) error {
	if options.Reverse != nil {
		return plugin.ErrUnsupported
	}
	return nil
}

func Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (*Session, error) {
	if options.Verify == nil || options.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if err := refuseReverse(options); err != nil {
		return nil, err
	}
	if options.Connect == nil || options.Timeout < 0 || options.Timeout/time.Millisecond > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	return openSession(ctx, d, options, func() (*Host, error) {
		return NewWithBackend(d, BackendOptions{Connect: options.Connect, Concurrent: true}, func([]byte) error { return nil }, func([]byte) error { return nil })
	})
}

// OpenProcess is Open with the child process owned by the C engine: it starts
// the launch described by launch, then handshakes. options.Connect is unused.
func OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	if options.Verify == nil || options.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if options.Timeout < 0 || options.Timeout/time.Millisecond > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	if options.Reverse != nil {
		if err := validateReverse(options.Reverse); err != nil {
			return nil, err
		}
	}
	return openSession(ctx, d, options, func() (*Host, error) {
		return NewLaunch(launch, d, func([]byte) error { return nil }, func([]byte) error { return nil })
	})
}

func openSession(ctx context.Context, d plugin.Descriptor, options plugin.Options, create func() (*Host, error)) (*Session, error) {
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
	h, err := create()
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
	if err = status(C.ext_go_set_hooks(h.ptr, C.uintptr_t(h.policy))); err != nil {
		h.Destroy()
		return nil, err
	}
	if options.Reverse != nil {
		if err = h.setReverse(options.Reverse); err != nil {
			h.Destroy()
			return nil, err
		}
	}
	if err = h.Start(ctx); err != nil {
		h.Destroy()
		return nil, err
	}
	return &Session{Host: h, done: make(chan struct{})}, nil
}
func (s *Session) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.State() == plugin.StateClosed {
		s.finish()
		return s.Host.Close()
	}
	if err := s.DrainContext(ctx); err != nil {
		return err
	}
	err := s.Host.Close()
	s.Destroy()
	s.finish()
	return err
}
func (s *Session) Abort() error {
	err := s.Host.Close()
	s.Destroy()
	s.finish()
	return err
}

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
