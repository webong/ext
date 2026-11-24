package clib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/endpoint"
)

// Prefix and ABIVersion name the library interface this package speaks: ext_plugin at
// EXT_PLUGIN_ABI_VERSION. A driver binds them (cengine/library.BindInterface(lib, Prefix, ABIVersion)).
const (
	Prefix     = "ext_plugin"
	ABIVersion = 1
)

// A Library is lib/plugin's library interface, however a driver reaches it: cengine/library's
// *Interface for purego. Its errors carry the library's code names (Coded).
type Library interface {
	Caller
	Open(config []byte) (uint64, error)
	Close(handle uint64) error
}

// Launch starts a reviewed executable as the plugin's child process, the way an installed package is
// run. The engine checks it: Executable must be the absolute path of a regular file, and the host
// environment is never inherited (Environment entries are added to a scrubbed base of PATH).
type Launch struct {
	Executable  string
	Arguments   []string
	Environment []string
	// Directory is the child's working directory; empty inherits the host's.
	Directory string
	// StderrBytes bounds the retained stderr; zero retains 4096 bytes.
	StderrBytes int
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
	if name := CodeName(err); codeErrors[name] != nil {
		return fmt.Errorf("%w: %w", err, codeErrors[name])
	}
	return err
}

// Services runs one of the engine's pure services (lib/plugin/services.md) on handle 0. No plugin
// is executed.
func Services(c Library, operation string, input json.RawMessage) (json.RawMessage, error) {
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

// OpenProcess admits d over a child process the engine starts, through c; options.Connect is unused.
// A nil Go policy is a denial; everything else the engine checks.
func OpenProcess(ctx context.Context, c Library, d engine.Descriptor, launch Launch, options engine.Options) (*Session, error) {
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
	s := &Session{c: c, handle: handle, descriptor: d.Clone(), options: options,
		base: context.WithoutCancel(ctx), done: make(chan struct{})}
	s.events = ServeEvents(c, handle, s.answer, s.notify)
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
		if verifyErr != nil && CodeName(err) == "denied" {
			return nil, verifyErr
		}
		return nil, engineError(err)
	}
	return s, nil
}

// Session is a session behind a host handle of the library interface.
type Session struct {
	c          Library
	handle     uint64
	descriptor engine.Descriptor
	options    engine.Options
	base       context.Context
	events     *Events

	mu         sync.Mutex
	tags       uint64
	onProgress map[string]func(engine.Progress) // the calls with progress in flight, by tag
	verifyErr  error

	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
}

// notify delivers the events that ask nothing: progress to the call in flight, observer events to
// the observer.
func (s *Session) notify(e Event) {
	switch e.Kind {
	case "progress":
		var p engine.Progress
		if engine.Decode(e.Data, &p) != nil {
			return
		}
		s.mu.Lock()
		f := s.onProgress[e.Tag]
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
func (s *Session) answer(e Event) map[string]any {
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

// Descriptor is the admitted plugin's descriptor.
func (s *Session) Descriptor() engine.Descriptor { return s.descriptor.Clone() }

// Done is closed once the session has been closed or aborted.
func (s *Session) Done() <-chan struct{} { return s.done }

// State is the session's state, as the engine reports it.
func (s *Session) State() engine.State {
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

// Stderr is the child's retained, bounded and possibly truncated stderr. Treat it as untrusted text.
func (s *Session) Stderr() string {
	out, err := s.c.Call(s.handle, "stderr", nil)
	if err != nil {
		return ""
	}
	var text string
	_ = json.Unmarshal(out, &text)
	return text
}

// Call runs operation on the plugin.
func (s *Session) Call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	return s.call(ctx, contract, operation, payload, nil, engine.ProgressLimits{})
}

// CallProgress is Call for an operation that declares progress: onProgress receives its events,
// validated and rate-limited by the engine, before CallProgress returns.
func (s *Session) CallProgress(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), options ...engine.CallOptions) (json.RawMessage, error) {
	var o engine.CallOptions
	if len(options) > 0 {
		o = options[0]
	}
	if err := o.Progress.Validate(); err != nil {
		return nil, err
	}
	return s.call(ctx, contract, operation, payload, onProgress, o.Progress)
}

// call runs one call. With onProgress, the call is tagged and its progress events go to it. With onProgress or
// an observer, it returns only once every event queued before the call ended has been delivered, so
// both run before the call returns, as in every other engine. A context that ends first returns at
// once; the engine finishes the call on its own and its result is dropped.
func (s *Session) call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), limits engine.ProgressLimits) (json.RawMessage, error) {
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
		s.mu.Lock()
		s.tags++
		tag := fmt.Sprintf("call-%d", s.tags)
		if s.onProgress == nil {
			s.onProgress = map[string]func(engine.Progress){}
		}
		s.onProgress[tag] = onProgress
		s.mu.Unlock()
		input["tag"] = tag
		defer func() {
			// The call's events are delivered before it returns; after that nothing reaches onProgress.
			s.mu.Lock()
			delete(s.onProgress, tag)
			s.mu.Unlock()
		}()
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

// Close drains the session and frees it.
func (s *Session) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.State() != engine.StateClosed {
		drain, _ := json.Marshal(map[string]any{"timeout_ms": callTimeout(ctx, s.options.Timeout)})
		_, err := s.c.Call(s.handle, "drain", drain)
		if s.options.Observer != nil {
			s.events.Fence()
		}
		if name := CodeName(err); err != nil && name != "closed" && name != "not_found" {
			return engineError(err)
		}
	}
	return s.Abort()
}

// Abort stops work and frees the session.
func (s *Session) Abort() error {
	s.closeOnce.Do(func() {
		if err := s.c.Close(s.handle); err != nil && CodeName(err) != "not_found" {
			s.closeErr = engineError(err)
		}
		<-s.events.Done()
		close(s.done)
	})
	return s.closeErr
}
