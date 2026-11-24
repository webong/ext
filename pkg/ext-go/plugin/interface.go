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

// This file is the Go host over lib/plugin's library interface: ext_plugin_open, _call and _close
// (lib/plugin/include/ext_plugin_host.h). It needs nothing else from the C engine, and no way for C
// to call Go: a host's callbacks (the policies, the observer, progress and reverse calls) are events
// a goroutine takes with "events" and answers with "answer". Whatever provides the four functions
// (purego now) runs a session through it.

// calls is the library interface of ext_plugin, however a driver reaches it.
type calls interface {
	Open(config []byte) (uint64, error)
	Call(handle uint64, op string, input []byte) ([]byte, error)
	Close(handle uint64) error
}

// The status codes of lib/plugin (ext_plugin_status), in the order of their enum.
var statusErrors = map[int32]error{
	1: engine.ErrInvalid, 2: engine.ErrDenied, 3: engine.ErrMismatch, 4: engine.ErrUnsupported,
	5: engine.ErrClosed, 6: context.DeadlineExceeded, 9: engine.ErrDraining, 10: engine.ErrNotFound,
	11: engine.ErrAmbiguous, 12: context.Canceled, 16: engine.ErrProtocol, 17: endpoint.ErrExpired,
}

const (
	statusClosed   = 5
	statusNotFound = 10
)

// engineError gives a code the library returned the engine error it stands for.
func engineError(err error) error {
	var code *library.CodeError
	if errors.As(err, &code) {
		if sentinel, ok := statusErrors[code.Code]; ok {
			return fmt.Errorf("%s: %w", code.Error(), sentinel)
		}
	}
	return err
}

func hasCode(err error, codes ...int32) bool {
	var code *library.CodeError
	if !errors.As(err, &code) {
		return false
	}
	for _, c := range codes {
		if code.Code == c {
			return true
		}
	}
	return false
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

// milliseconds is d in whole milliseconds for the interface: at least 1, at most MaxUint32.
func milliseconds(d time.Duration) uint64 {
	ms := d.Milliseconds()
	if ms < 1 {
		return 1
	}
	if ms > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint64(ms)
}

// callTimeout bounds a call by the earlier of the context's deadline and the session's timeout
// (DefaultTimeout when zero).
func callTimeout(ctx context.Context, timeout time.Duration) uint64 {
	if timeout <= 0 {
		timeout = engine.DefaultTimeout
	}
	if until, ok := ctx.Deadline(); ok && time.Until(until) < timeout {
		timeout = time.Until(until)
	}
	return milliseconds(timeout)
}

func validReverse(o *engine.ReverseOptions) error {
	if o.Handle == nil || o.Authorize == nil {
		return engine.ErrDenied
	}
	if o.Timeout < 0 || o.MaxPayloadBytes < 0 || o.MaxPayloadBytes > engine.MaxFrameBytes || o.MaxInFlight < 0 || o.MaxInFlight > 1024 {
		return engine.ErrInvalid
	}
	return nil
}

// openInterfaceSession admits d over a child process the engine starts, through c.
func openInterfaceSession(ctx context.Context, name Name, c calls, d engine.Descriptor, launch Launch, options engine.Options) (Session, error) {
	if options.Verify == nil || options.Authorize == nil {
		return nil, engine.ErrDenied
	}
	if options.Timeout < 0 || options.Timeout/time.Millisecond > math.MaxUint32 || launch.StderrBytes < 0 {
		return nil, engine.ErrInvalid
	}
	if options.Reverse != nil {
		if err := validReverse(options.Reverse); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requirements := make([]map[string]any, 0, len(options.Requirements))
	for _, r := range options.Requirements {
		value := map[string]any{"contract": r.Contract, "operation": r.Operation}
		if r.Identity != nil {
			value["identity"] = r.Identity
		}
		requirements = append(requirements, value)
	}
	check, err := json.Marshal(map[string]any{"descriptor": d, "requirements": requirements})
	if err != nil {
		return nil, err
	}
	if _, err := interfaceServices(c, "descriptor.requirements", check); err != nil {
		return nil, err
	}

	backend := map[string]any{"kind": "process", "executable": launch.Executable}
	if len(launch.Arguments) > 0 {
		backend["arguments"] = launch.Arguments
	}
	if len(launch.Environment) > 0 {
		backend["environment"] = launch.Environment
	}
	if launch.Directory != "" {
		backend["working_directory"] = launch.Directory
	}
	if launch.StderrBytes > 0 {
		backend["stderr_bytes"] = launch.StderrBytes
	}
	config := map[string]any{
		"object": "host", "descriptor": d, "backend": backend,
		"verify": "event", "authorize": "event", "observe": options.Observer != nil,
	}
	if r := options.Reverse; r != nil {
		services := map[string]any{}
		if r.Timeout > 0 {
			services["timeout_ms"] = milliseconds(r.Timeout)
		}
		if r.MaxPayloadBytes > 0 {
			services["max_payload_bytes"] = r.MaxPayloadBytes
		}
		if r.MaxInFlight > 0 {
			services["max_in_flight"] = r.MaxInFlight
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
	s := &interfaceSession{
		name: name, c: c, handle: handle, descriptor: d.Clone(), options: options,
		base: context.WithoutCancel(ctx), done: make(chan struct{}), loopDone: make(chan struct{}),
	}
	s.handled.L = &s.mu
	go s.loop()
	start, _ := json.Marshal(map[string]any{"timeout_ms": callTimeout(ctx, options.Timeout)})
	_, err = c.Call(handle, "start", start)
	if options.Observer != nil {
		s.fence()
	}
	if err != nil {
		s.mu.Lock()
		verifyErr := s.verifyErr
		s.mu.Unlock()
		_ = s.Abort()
		if verifyErr != nil && hasCode(err, 2) {
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

	// progress allows one call with progress at a time: the engine's progress events do not say
	// which call they belong to.
	progress sync.Mutex

	mu         sync.Mutex
	handled    sync.Cond // signalled when seen grows
	seen       uint64    // the seq of the last event the loop has handled
	onProgress func(engine.Progress)
	verifyErr  error

	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
	loopDone  chan struct{}
}

type hostEvent struct {
	Seq    uint64          `json:"seq"`
	Ticket uint64          `json:"ticket"`
	Kind   string          `json:"kind"`
	Data   json.RawMessage `json:"data"`
}

// loop takes the host's events until the host is closed. Events that ask are answered on their
// own goroutines, so a slow policy holds up nothing else; progress and observer events are
// delivered here, in order.
func (s *interfaceSession) loop() {
	defer func() {
		s.mu.Lock()
		s.seen = math.MaxUint64
		s.handled.Broadcast()
		s.mu.Unlock()
		close(s.loopDone)
	}()
	for {
		out, err := s.c.Call(s.handle, "events", []byte(`{"timeout_ms":250,"max":64}`))
		if err != nil {
			if hasCode(err, statusClosed, statusNotFound) {
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var events []hostEvent
		if err := json.Unmarshal(out, &events); err != nil {
			continue
		}
		for _, e := range events {
			switch e.Kind {
			case "verify", "authorize", "reverse_authorize", "reverse":
				go s.answer(e)
			case "progress":
				s.deliverProgress(e.Data)
			case "observe":
				s.observe(e.Data)
			}
			if e.Seq != 0 {
				s.mu.Lock()
				if e.Seq > s.seen {
					s.seen = e.Seq
				}
				s.handled.Broadcast()
				s.mu.Unlock()
			}
		}
	}
}

func (s *interfaceSession) deliverProgress(data json.RawMessage) {
	var p engine.Progress
	if engine.Decode(data, &p) != nil {
		return
	}
	s.mu.Lock()
	f := s.onProgress
	s.mu.Unlock()
	if f != nil {
		func() {
			defer func() { _ = recover() }()
			f(p)
		}()
	}
}

func (s *interfaceSession) observe(data json.RawMessage) {
	if s.options.Observer == nil {
		return
	}
	var e struct {
		Stage     string             `json:"stage"`
		Identity  engine.Identity    `json:"identity"`
		Contract  engine.ContractRef `json:"contract"`
		Operation string             `json:"operation"`
		RequestID string             `json:"requestID"`
		Code      string             `json:"code"`
		Duration  int64              `json:"durationMilliseconds"`
	}
	if engine.Decode(data, &e) != nil {
		return
	}
	defer func() { _ = recover() }()
	s.options.Observer(s.base, engine.Event{
		Stage: e.Stage, Identity: e.Identity, Contract: e.Contract, Operation: e.Operation,
		RequestID: e.RequestID, Code: e.Code, Duration: time.Duration(e.Duration) * time.Millisecond,
	})
}

// requestContext is the context a policy or a reverse handler runs in: the session's, bounded by the
// request's deadline.
func (s *interfaceSession) requestContext(r engine.Request) (context.Context, context.CancelFunc) {
	if r.Deadline.IsZero() {
		return context.WithTimeout(s.base, engine.DefaultTimeout)
	}
	return context.WithDeadline(s.base, r.Deadline)
}

// answer answers an event that asks. A policy that fails, or panics, denies.
func (s *interfaceSession) answer(e hostEvent) {
	reply := map[string]any{"ticket": e.Ticket, "allow": false}
	defer func() {
		_ = recover()
		raw, err := json.Marshal(reply)
		if err == nil {
			// The engine may have stopped waiting (a timeout, a closed host): nothing to do then.
			_, _ = s.c.Call(s.handle, "answer", raw)
		}
	}()
	switch e.Kind {
	case "verify":
		var d engine.Descriptor
		err := engine.Decode(e.Data, &d)
		if err == nil {
			err = s.options.Verify(s.base, d)
		}
		s.mu.Lock()
		s.verifyErr = err
		s.mu.Unlock()
		reply["allow"] = err == nil
	case "authorize", "reverse_authorize", "reverse":
		var r engine.Request
		if engine.Decode(e.Data, &r) != nil {
			return
		}
		ctx, cancel := s.requestContext(r)
		defer cancel()
		switch e.Kind {
		case "authorize":
			reply["allow"] = s.options.Authorize(ctx, r.Clone()) == nil
		case "reverse_authorize":
			reply["allow"] = s.options.Reverse != nil && s.options.Reverse.Authorize(ctx, r.Clone()) == nil
		case "reverse":
			delete(reply, "allow")
			reply["error"] = &engine.RemoteError{Code: "operation_failed", Message: "the host's handler failed"}
			if s.options.Reverse == nil {
				return
			}
			payload, err := s.options.Reverse.Handle(ctx, r.Clone())
			var remote *engine.RemoteError
			switch {
			case err == nil:
				if len(payload) == 0 {
					payload = json.RawMessage("null")
				}
				delete(reply, "error")
				reply["payload"] = payload
			case errors.As(err, &remote) && remote != nil:
				reply["error"] = remote
			}
		}
	}
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
	if json.Unmarshal(out, &state) != nil {
		return engine.StateFailed
	}
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
// an observer, it returns only once the loop has delivered every event queued before the call
// ended, so both run before the call returns, as in every other engine. A context that ends first
// returns at once; the engine finishes the call on its own and its result is dropped.
func (s *interfaceSession) call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), limits engine.ProgressLimits) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input := map[string]any{
		"call":       map[string]any{"contract": contract, "operation": operation},
		"timeout_ms": callTimeout(ctx, s.options.Timeout),
	}
	if len(payload) > 0 {
		input["call"].(map[string]any)["payload"] = payload
	}
	if onProgress != nil {
		progress := map[string]any{}
		if limits.EventsPerSecond > 0 {
			progress["events_per_second"] = limits.EventsPerSecond
		}
		if limits.BytesPerSecond > 0 {
			progress["bytes_per_second"] = limits.BytesPerSecond
		}
		input["progress"] = progress
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
			s.mu.Lock()
			s.onProgress = onProgress
			s.mu.Unlock()
			defer func() {
				s.mu.Lock()
				s.onProgress = nil
				s.mu.Unlock()
			}()
		}
		out, err := s.c.Call(s.handle, "call", raw)
		if onProgress != nil || s.options.Observer != nil {
			s.fence()
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

// fence waits until the loop has handled every event the host had queued when it is called.
func (s *interfaceSession) fence() {
	out, err := s.c.Call(s.handle, "sequence", nil)
	if err != nil {
		return
	}
	var last uint64
	if json.Unmarshal(out, &last) != nil {
		return
	}
	s.mu.Lock()
	for s.seen < last {
		s.handled.Wait()
	}
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
			s.fence()
		}
		if err != nil && !hasCode(err, statusClosed, statusNotFound) {
			return engineError(err)
		}
	}
	return s.Abort()
}

func (s *interfaceSession) Abort() error {
	s.closeOnce.Do(func() {
		if err := s.c.Close(s.handle); err != nil && !hasCode(err, statusNotFound) {
			s.closeErr = engineError(err)
		}
		<-s.loopDone
		close(s.done)
	})
	return s.closeErr
}
