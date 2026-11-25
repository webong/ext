// Package plugin adapts Go transports and policies to the canonical C host.
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

type Library struct{ api *library.Described }

// Objects exposes the canonical C library interface for operations beyond sessions.
func (lib *Library) Objects() *library.Described { return lib.api }

func Bind(native *library.Library) (*Library, error) {
	i, e := library.BindInterface(native, "ext_plugin", 1)
	if e != nil {
		return nil, e
	}
	d, e := library.Describe(i)
	if e != nil {
		return nil, e
	}
	return &Library{api: d}, nil
}
func allow(fn func(json.RawMessage) error) library.Handler {
	return func(data json.RawMessage) (any, error) { return nil, fn(data) }
}
func (lib *Library) Open(ctx context.Context, d engine.Descriptor, o engine.Options) (*Session, error) {
	if o.Connect == nil {
		return nil, engine.ErrInvalid
	}
	return lib.OpenProcess(ctx, d, Launch{}, o)
}
func (s *Session) Descriptor() engine.Descriptor { return s.descriptor.Clone() }

var ErrRegistryExists = errors.New("plugin registry entry already exists")

// codeErrors are the engine errors lib/plugin's code names stand for.
var codeErrors = map[string]error{
	"exists": ErrRegistryExists, "invalid": engine.ErrInvalid, "denied": engine.ErrDenied, "mismatch": engine.ErrMismatch,
	"unsupported": engine.ErrUnsupported, "closed": engine.ErrClosed, "timeout": context.DeadlineExceeded,
	"draining": engine.ErrDraining, "not_found": engine.ErrNotFound, "ambiguous": engine.ErrAmbiguous,
	"canceled": context.Canceled, "protocol": engine.ErrProtocol, "expired": endpoint.ErrExpired,
}

func codeName(err error) string {
	var code *library.CodeError
	if errors.As(err, &code) {
		return code.Name
	}
	return ""
}

func engineError(err error) error {
	if sentinel := codeErrors[codeName(err)]; sentinel != nil {
		return fmt.Errorf("%w: %w", err, sentinel)
	}
	return err
}

func timeoutMS(ctx context.Context, timeout time.Duration) int64 {
	if timeout <= 0 {
		timeout = engine.DefaultTimeout
	}
	if until, ok := ctx.Deadline(); ok && time.Until(until) < timeout {
		timeout = time.Until(until)
	}
	return min(max(timeout.Milliseconds(), 1), math.MaxUint32)
}

// Launch is a child process the engine starts.
type Launch struct {
	Executable  string
	Arguments   []string
	Environment []string
	Directory   string
	StderrBytes int
}

type Session struct {
	terminal      engine.State
	registry      *Registry
	life          context.Context
	cancel        context.CancelFunc
	start         context.Context
	backend       engine.Backend
	backendClosed bool
	host          *library.Object
	descriptor    engine.Descriptor
	options       engine.Options
	base          context.Context
	mu            sync.Mutex
	verifyErr     error
	once          sync.Once
	closeErr      error
	done          chan struct{}
}

func (lib *Library) OpenProcess(ctx context.Context, d engine.Descriptor, launch Launch, options engine.Options) (*Session, error) {
	return lib.open(ctx, d, launch, options, nil)
}
func (lib *Library) open(ctx context.Context, d engine.Descriptor, launch Launch, options engine.Options, registry *Registry) (*Session, error) {
	if options.Verify == nil || options.Authorize == nil || (options.Reverse != nil && ((options.Reverse.Handle == nil && registry == nil) || options.Reverse.Authorize == nil)) {
		return nil, engine.ErrDenied
	}
	if options.Timeout < 0 {
		return nil, engine.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptor, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var requirements []json.RawMessage
	for _, r := range options.Requirements {
		value := map[string]any{"contract": r.Contract, "operation": r.Operation}
		if r.Identity != nil {
			value["identity"] = r.Identity
		}
		raw, _ := json.Marshal(value)
		requirements = append(requirements, raw)
	}
	s := &Session{descriptor: d.Clone(), options: options, base: context.WithoutCancel(ctx), done: make(chan struct{})}
	config := map[string]any{"descriptor": json.RawMessage(descriptor), "verify": "event", "authorize": "event", "observe": options.Observer != nil, "requirements": requirements, "event_timeout_ms": timeoutMS(context.Background(), options.Timeout),
		"backend": map[string]any{"kind": "process", "executable": launch.Executable, "arguments": launch.Arguments, "environment": launch.Environment, "working_directory": launch.Directory, "stderr_bytes": launch.StderrBytes}}
	backendConfig := config["backend"].(map[string]any)
	if len(launch.Arguments) == 0 {
		delete(backendConfig, "arguments")
	}
	if len(launch.Environment) == 0 {
		delete(backendConfig, "environment")
	}
	if launch.Directory == "" {
		delete(backendConfig, "working_directory")
	}
	if launch.StderrBytes < 0 {
		return nil, engine.ErrInvalid
	}
	if len(requirements) == 0 {
		delete(config, "requirements")
	}
	if launch.Executable == "" {
		config["backend"] = map[string]any{"kind": "events"}
	}
	handlers := library.Handlers{"verify": allow(s.verify), "authorize": allow(s.policy(func() func(context.Context, engine.Request) error { return options.Authorize })),
		"backend_connect": s.connect, "backend_invoke": s.invoke, "backend_close": func(json.RawMessage) (any, error) { s.closeBackend(); return nil, nil }}
	if options.Observer != nil {
		handlers["observe"] = func(data json.RawMessage) (any, error) { s.observe(data); return nil, nil }
	}
	if r := options.Reverse; r != nil {
		config["services"] = map[string]any{"max_payload_bytes": r.MaxPayloadBytes, "max_in_flight": r.MaxInFlight, "timeout_ms": timeoutMS(context.Background(), r.Timeout)}
		handlers["reverse_authorize"] = allow(s.policy(func() func(context.Context, engine.Request) error { return r.Authorize }))
		handlers["reverse"] = s.reverse
	}
	s.life, s.cancel = context.WithCancel(context.WithoutCancel(ctx))
	s.start = ctx
	s.registry = registry
	if registry != nil {
		config["registry"] = registry.object.Handle()
		delete(handlers, "reverse")
	}
	if s.host, err = lib.api.Open("host", config, handlers); err != nil {
		s.cancel()
		return nil, engineError(err)
	}

	if err := s.host.Call("start", map[string]any{"timeout_ms": timeoutMS(ctx, options.Timeout)}, nil); err != nil {
		s.mu.Lock()
		verifyErr := s.verifyErr
		s.mu.Unlock()
		_ = s.Abort()
		if verifyErr != nil && codeName(err) == "denied" {
			return nil, fmt.Errorf("%w: %w", engine.ErrDenied, verifyErr)
		}
		return nil, engineError(err)
	}
	return s, nil
}

func (s *Session) verify(data json.RawMessage) error {
	var d engine.Descriptor
	err := engine.Decode(data, &d)
	if err == nil {
		err = s.options.Verify(s.start, d)
	}
	s.mu.Lock()
	s.verifyErr = err
	s.mu.Unlock()
	return err
}

func (s *Session) policy(f func() func(context.Context, engine.Request) error) func(json.RawMessage) error {
	return s.policyIn(s.base, f)
}

// policy answers a request with a Go policy, in the session's context bounded by the request's
// deadline.
func (s *Session) policyIn(ctx context.Context, f func() func(context.Context, engine.Request) error) func(json.RawMessage) error {
	return func(data json.RawMessage) error {
		var r engine.Request
		if err := engine.Decode(data, &r); err != nil {
			return err
		}
		ctx, cancel := context.WithDeadline(ctx, r.Deadline)
		defer cancel()
		return f()(ctx, r.Clone())
	}
}

func (s *Session) reverse(data json.RawMessage) (any, error) { return s.reverseIn(s.base, data) }
func (s *Session) reverseIn(ctx context.Context, data json.RawMessage) (any, error) {
	var r engine.Request
	if err := engine.Decode(data, &r); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithDeadline(ctx, r.Deadline)
	defer cancel()
	payload, err := s.options.Reverse.Handle(ctx, r.Clone())
	if err != nil {
		return nil, err // a RemoteError marshals to {code, message} and is passed on
	}
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	return payload, nil
}

func (s *Session) observe(data json.RawMessage) { s.observeIn(s.base, data) }
func (s *Session) observeIn(ctx context.Context, data json.RawMessage) {
	var o struct {
		Stage     string             `json:"stage"`
		Identity  engine.Identity    `json:"identity"`
		Contract  engine.ContractRef `json:"contract"`
		Operation string             `json:"operation"`
		RequestID string             `json:"requestID"`
		Code      string             `json:"code"`
		Duration  int64              `json:"durationMilliseconds"`
	}
	if engine.Decode(data, &o) == nil {
		if o.Stage == "verify" && o.Code == "denied" {
			s.mu.Lock()
			v := s.verifyErr
			s.mu.Unlock()
			if v != nil {
				o.Code = "failed"
				if errors.Is(v, engine.ErrDenied) {
					o.Code = "denied"
				}
			}
		}
		s.options.Observer(ctx, engine.Event{Stage: o.Stage, Identity: o.Identity, Contract: o.Contract, Operation: o.Operation,
			RequestID: o.RequestID, Code: o.Code, Duration: time.Duration(o.Duration) * time.Millisecond})
	}
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) State() engine.State {
	s.mu.Lock()
	terminal := s.terminal
	s.mu.Unlock()
	if terminal != "" {
		return terminal
	}
	var state string
	err := s.host.Call("state", nil, &state)
	if err != nil {
		return engine.StateClosed
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

func (s *Session) Stderr() string {
	var text string
	_ = s.host.Call("stderr", nil, &text)
	return text
}

func (s *Session) Call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	return s.call(ctx, contract, operation, payload, nil, engine.ProgressLimits{})
}

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

func (s *Session) call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), limits engine.ProgressLimits) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		s.rejected(ctx, contract, operation, err)
		return nil, err
	}
	select {
	case <-s.done:
		s.rejected(ctx, contract, operation, engine.ErrClosed)
		return nil, engine.ErrClosed
	default:
	}
	call := map[string]any{"contract": contract, "operation": operation}
	if len(payload) > 0 {
		call["payload"] = payload
	}
	raw, err := json.Marshal(call)
	if err != nil {
		return nil, err
	}
	in := map[string]any{"call": json.RawMessage(raw), "timeout_ms": timeoutMS(ctx, s.options.Timeout)}
	type result struct {
		out json.RawMessage
		err error
	}
	finished := make(chan result, 1)
	go func() {
		var r result
		var transportErr error
		handlers := library.Handlers{
			"authorize": allow(s.policyIn(ctx, func() func(context.Context, engine.Request) error { return s.options.Authorize })),
			"backend_invoke": func(data json.RawMessage) (any, error) {
				response, err := s.invokeIn(ctx, data)
				transportErr = err
				return response, backendError(err)
			},
		}
		if reverse := s.options.Reverse; reverse != nil {
			handlers["reverse_authorize"] = allow(s.policyIn(ctx, func() func(context.Context, engine.Request) error { return reverse.Authorize }))
			if s.registry == nil {
				handlers["reverse"] = func(data json.RawMessage) (any, error) { return s.reverseIn(ctx, data) }
			}
		}
		if s.options.Observer != nil {
			handlers["observe"] = func(data json.RawMessage) (any, error) { s.observeIn(ctx, data); return nil, nil }
		}
		if onProgress != nil {
			in["progress"] = map[string]any{"events_per_second": limits.EventsPerSecond, "bytes_per_second": limits.BytesPerSecond}
			handlers["progress"] = func(data json.RawMessage) (any, error) {
				var p engine.Progress
				if engine.Decode(data, &p) == nil {
					onProgress(p)
				}
				return nil, nil
			}
		}
		r.err = s.host.CallWith("call", in, &r.out, handlers)
		if r.err != nil && transportErr != nil {
			r.err = errors.Join(r.err, transportErr)
		}
		finished <- r
	}()
	var r result
	select {
	case r = <-finished:
	case <-ctx.Done():
		_ = s.Abort()
		return nil, ctx.Err()
	}
	if r.err != nil {
		if state := s.State(); state == engine.StateFailed || state == engine.StateClosed {
			s.mu.Lock()
			s.terminal = state
			s.mu.Unlock()
			_ = s.Abort()
		}
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

func (s *Session) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.State() != engine.StateClosed {
		err := s.host.Call("drain", map[string]any{"timeout_ms": timeoutMS(ctx, s.options.Timeout)}, nil)
		if name := codeName(err); err != nil && name != "closed" && name != "not_found" {
			return engineError(err)
		}
	}
	return s.Abort()
}

func (s *Session) Abort() error {
	s.once.Do(func() {
		s.cancel()
		s.closeBackend()
		if err := s.host.Close(); err != nil && codeName(err) != "not_found" {
			s.closeErr = engineError(err)
		}
		close(s.done)
	})
	return s.closeErr
}

// Rejections made before a native handle can be entered still belong to the Go
// caller's observer contract. All admitted calls are observed by the C host.
func (s *Session) rejected(ctx context.Context, contract engine.ContractRef, operation string, err error) {
	if s.options.Observer == nil {
		return
	}
	code := "closed"
	if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "deadline_exceeded"
	}
	defer func() { _ = recover() }()
	s.options.Observer(ctx, engine.Event{Stage: "invoke", Identity: s.descriptor.Identity, Contract: contract, Operation: operation, Code: code})
}
