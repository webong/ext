package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/endpoint"
)

// This file is the Go binding of lib/plugin's library interface (ext_plugin_open, _call and _close;
// lib/plugin/include/ext_plugin_host.h). The rules are the C engine's: the binding only turns Go
// values into the interface's JSON and back, and a Go policy, observer or reverse handler into the
// answer to an event, through cengine/library.ServeEvents.

// calls is lib/plugin's library interface, however a driver reaches it.
type calls interface {
	library.Caller
	Open(config []byte) (uint64, error)
	Close(handle uint64) error
}

// codeErrors are the engine errors lib/plugin's code names stand for.
var codeErrors = map[string]error{
	"invalid": engine.ErrInvalid, "denied": engine.ErrDenied, "mismatch": engine.ErrMismatch,
	"unsupported": engine.ErrUnsupported, "closed": engine.ErrClosed, "timeout": context.DeadlineExceeded,
	"draining": engine.ErrDraining, "not_found": engine.ErrNotFound, "ambiguous": engine.ErrAmbiguous,
	"canceled": context.Canceled, "protocol": engine.ErrProtocol, "expired": endpoint.ErrExpired,
}

// engineError gives a code the library returned the engine error it stands for.
func engineError(err error) error {
	if name := codeName(err); codeErrors[name] != nil {
		return fmt.Errorf("%w: %w", err, codeErrors[name])
	}
	return err
}

func codeName(err error) string {
	var code *library.CodeError
	if errors.As(err, &code) {
		return code.Name
	}
	return ""
}

// interfaceServices runs one of the engine's pure services on handle 0.
func interfaceServices(c calls, operation string, input json.RawMessage) (json.RawMessage, error) {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	out, err := c.Call(0, operation, input)
	if err != nil {
		return nil, engineError(err)
	}
	return out, nil
}

// callTimeout is the earlier of the context's deadline and timeout (DefaultTimeout when zero), in
// whole milliseconds, at least 1.
func callTimeout(ctx context.Context, timeout time.Duration) uint64 {
	if timeout <= 0 {
		timeout = engine.DefaultTimeout
	}
	if until, ok := ctx.Deadline(); ok && time.Until(until) < timeout {
		timeout = time.Until(until)
	}
	return uint64(min(max(timeout.Milliseconds(), 1), math.MaxUint32))
}

// openInterfaceSession admits d over a child process the engine starts, through c. A nil Go policy
// is a denial; everything else the engine checks.
func openInterfaceSession(ctx context.Context, name Name, c calls, d engine.Descriptor, launch Launch, options engine.Options) (Session, error) {
	if options.Verify == nil || options.Authorize == nil || (options.Reverse != nil && (options.Reverse.Handle == nil || options.Reverse.Authorize == nil)) {
		return nil, engine.ErrDenied
	}
	if options.Timeout < 0 || (options.Reverse != nil && options.Reverse.Timeout < 0) {
		return nil, engine.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend := map[string]any{"kind": "process", "executable": launch.Executable, "arguments": append([]string{}, launch.Arguments...),
		"environment": append([]string{}, launch.Environment...), "stderr_bytes": launch.StderrBytes}
	if launch.Directory != "" {
		backend["working_directory"] = launch.Directory
	}
	requirements := make([]map[string]any, 0, len(options.Requirements))
	for _, r := range options.Requirements {
		value := map[string]any{"contract": r.Contract, "operation": r.Operation}
		if r.Identity != nil {
			value["identity"] = r.Identity
		}
		requirements = append(requirements, value)
	}
	config := map[string]any{
		"object": "host", "descriptor": d, "backend": backend, "requirements": requirements,
		"verify": "event", "authorize": "event", "observe": options.Observer != nil,
	}
	if r := options.Reverse; r != nil {
		services := map[string]any{"max_payload_bytes": r.MaxPayloadBytes, "max_in_flight": r.MaxInFlight}
		if r.Timeout > 0 {
			services["timeout_ms"] = callTimeout(context.Background(), r.Timeout)
		}
		config["services"] = services
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	handle, err := c.Open(raw)
	if err != nil {
		return nil, engineError(err)
	}
	s := &interfaceSession{name: name, c: c, handle: handle, descriptor: d.Clone(), options: options,
		base: context.WithoutCancel(ctx), done: make(chan struct{})}
	s.events = library.ServeEvents(c, handle, s.answer, s.notify)
	start, _ := json.Marshal(map[string]any{"timeout_ms": callTimeout(ctx, options.Timeout)})
	_, err = c.Call(handle, "start", start)
	if options.Observer != nil {
		s.events.Fence()
	}
	if err != nil {
		s.mu.Lock()
		verifyErr := s.verifyErr
		s.mu.Unlock()
		_ = s.Abort()
		if verifyErr != nil && codeName(err) == "denied" {
			return nil, verifyErr
		}
		return nil, engineError(err)
	}
	return s, nil
}

// interfaceSession is a session behind a host handle of the library interface.
type interfaceSession struct {
	name       Name
	c          calls
	handle     uint64
	descriptor engine.Descriptor
	options    engine.Options
	base       context.Context
	events     *library.Events

	// progress allows one call with progress at a time: the engine's progress events do not say
	// which call they belong to.
	progress sync.Mutex

	mu         sync.Mutex
	onProgress func(engine.Progress)
	verifyErr  error

	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
}

// notify delivers the events that ask nothing: progress to the call in flight, observer events to
// the observer.
func (s *interfaceSession) notify(e library.Event) {
	switch e.Kind {
	case "progress":
		var p engine.Progress
		if engine.Decode(e.Data, &p) != nil {
			return
		}
		s.mu.Lock()
		f := s.onProgress
		s.mu.Unlock()
		if f != nil {
			f(p)
		}
	case "observe":
		if s.options.Observer == nil {
			return
		}
		var o struct {
			Stage     string             `json:"stage"`
			Identity  engine.Identity    `json:"identity"`
			Contract  engine.ContractRef `json:"contract"`
			Operation string             `json:"operation"`
			RequestID string             `json:"requestID"`
			Code      string             `json:"code"`
			Duration  int64              `json:"durationMilliseconds"`
		}
		if engine.Decode(e.Data, &o) != nil {
			return
		}
		s.options.Observer(s.base, engine.Event{Stage: o.Stage, Identity: o.Identity, Contract: o.Contract,
			Operation: o.Operation, RequestID: o.RequestID, Code: o.Code, Duration: time.Duration(o.Duration) * time.Millisecond})
	}
}

// answer answers an event that asks with the Go policy or handler for it; nil is a denial.
func (s *interfaceSession) answer(e library.Event) map[string]any {
	if e.Kind == "verify" {
		var d engine.Descriptor
		err := engine.Decode(e.Data, &d)
		if err == nil {
			err = s.options.Verify(s.base, d)
		}
		s.mu.Lock()
		s.verifyErr = err
		s.mu.Unlock()
		return map[string]any{"allow": err == nil}
	}
	var r engine.Request
	if engine.Decode(e.Data, &r) != nil {
		return nil
	}
	// A policy or reverse handler runs in the session's context, bounded by the request's deadline.
	ctx, cancel := context.WithDeadline(s.base, r.Deadline)
	defer cancel()
	switch e.Kind {
	case "authorize":
		return map[string]any{"allow": s.options.Authorize(ctx, r.Clone()) == nil}
	case "reverse_authorize":
		return map[string]any{"allow": s.options.Reverse != nil && s.options.Reverse.Authorize(ctx, r.Clone()) == nil}
	case "reverse":
		if s.options.Reverse == nil {
			return nil
		}
		payload, err := s.options.Reverse.Handle(ctx, r.Clone())
		var remote *engine.RemoteError
		switch {
		case err == nil:
			if len(payload) == 0 {
				payload = json.RawMessage("null")
			}
			return map[string]any{"payload": payload}
		case errors.As(err, &remote) && remote != nil:
			return map[string]any{"error": remote}
		}
		return map[string]any{"error": &engine.RemoteError{Code: "operation_failed", Message: "the host's handler failed"}}
	}
	return nil
}

func (s *interfaceSession) Engine() Name                  { return s.name }
func (s *interfaceSession) Descriptor() engine.Descriptor { return s.descriptor.Clone() }
func (s *interfaceSession) Done() <-chan struct{}         { return s.done }

func (s *interfaceSession) State() engine.State {
	out, err := s.c.Call(s.handle, "state", nil)
	if err != nil {
		return engine.StateClosed
	}
	var state string
	_ = json.Unmarshal(out, &state)
	switch state {
	case "created", "ready":
		return engine.StateReady
	case "draining":
		return engine.StateDraining
	case "closed":
		return engine.StateClosed
	}
	return engine.StateFailed
}

func (s *interfaceSession) Stderr() string {
	out, err := s.c.Call(s.handle, "stderr", nil)
	if err != nil {
		return ""
	}
	var text string
	_ = json.Unmarshal(out, &text)
	return text
}

func (s *interfaceSession) Call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	return s.call(ctx, contract, operation, payload, nil, engine.ProgressLimits{})
}

func (s *interfaceSession) CallProgress(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), options ...engine.CallOptions) (json.RawMessage, error) {
	var o engine.CallOptions
	if len(options) > 0 {
		o = options[0]
	}
	if err := o.Progress.Validate(); err != nil {
		return nil, err
	}
	return s.call(ctx, contract, operation, payload, onProgress, o.Progress)
}

// call runs one call. With onProgress, it holds the session's one progress slot. With onProgress or
// an observer, it returns only once every event queued before the call ended has been delivered, so
// both run before the call returns, as in every other engine. A context that ends first returns at
// once; the engine finishes the call on its own and its result is dropped.
func (s *interfaceSession) call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), limits engine.ProgressLimits) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := map[string]any{"contract": contract, "operation": operation}
	if len(payload) > 0 {
		call["payload"] = payload
	}
	input := map[string]any{"call": call, "timeout_ms": callTimeout(ctx, s.options.Timeout)}
	if onProgress != nil {
		input["progress"] = map[string]any{"events_per_second": limits.EventsPerSecond, "bytes_per_second": limits.BytesPerSecond}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	type result struct {
		out []byte
		err error
	}
	finished := make(chan result, 1)
	go func() {
		if onProgress != nil {
			s.progress.Lock()
			defer s.progress.Unlock()
			s.setProgress(onProgress)
			defer s.setProgress(nil)
		}
		out, err := s.c.Call(s.handle, "call", raw)
		if onProgress != nil || s.options.Observer != nil {
			s.events.Fence()
		}
		finished <- result{out, err}
	}()
	var r result
	select {
	case r = <-finished:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, engineError(r.err)
	}
	var response engine.Response
	if err := engine.Decode(r.out, &response); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, response.Error
	}
	return response.Payload, nil
}

func (s *interfaceSession) setProgress(f func(engine.Progress)) {
	s.mu.Lock()
	s.onProgress = f
	s.mu.Unlock()
}

func (s *interfaceSession) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.State() != engine.StateClosed {
		drain, _ := json.Marshal(map[string]any{"timeout_ms": callTimeout(ctx, s.options.Timeout)})
		_, err := s.c.Call(s.handle, "drain", drain)
		if s.options.Observer != nil {
			s.events.Fence()
		}
		if name := codeName(err); err != nil && name != "closed" && name != "not_found" {
			return engineError(err)
		}
	}
	return s.Abort()
}

func (s *interfaceSession) Abort() error {
	s.closeOnce.Do(func() {
		if err := s.c.Close(s.handle); err != nil && codeName(err) != "not_found" {
			s.closeErr = engineError(err)
		}
		<-s.events.Done()
		close(s.done)
	})
	return s.closeErr
}
