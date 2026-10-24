//go:build (darwin && amd64) || (darwin && arm64) || (linux && arm64)

package ccgo

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// This file is the Go side of a session over the translated host, the counterpart of the cgo
// binding's session.go, engine.go and backend.go in pkg/plugin-go/cengine: how a Go context becomes
// native call options and a cancel signal, how Go errors survive the trip through status codes, how
// a plugin.Backend becomes the engine's extension backend, and how events and policies reach
// plugin.Options. It does not carry progress events or reverse calls yet: a plugin that declares
// them is refused when the host is created, and Options.Reverse is unsupported.

// Session is a plugin session whose engine is the translated C host. It implements author.Caller.
// Close drains and frees the native allocation; Abort stops work and then frees it.
type Session struct {
	mu          sync.RWMutex
	host        *Host
	policies    *policies
	descriptor  plugin.Descriptor
	callTimeout time.Duration
	backend     *backendBinding
	done        chan struct{}
	doneOnce    sync.Once
}

// policies are the Go callbacks a session's hooks reach.
type policies struct {
	verifyContext, authorizeContext func(context.Context, []byte) error
	observer                        plugin.Observer
	// verifyErr is what the verify policy returned. The engine knows only that it denied, and the
	// Go session names the stage's event after the policy's own error, so the observer needs it.
	verifyMu  sync.Mutex
	verifyErr error
}

// Done is closed once the session has been closed or aborted.
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) finish() { s.doneOnce.Do(func() { close(s.done) }) }

// Descriptor returns a detached copy of the immutable selection.
func (s *Session) Descriptor() plugin.Descriptor { return s.descriptor.Clone() }

// Errors stay within the Go caller's scope; only bounded status codes cross into the engine. A
// per-call recorder keeps concurrent invocations from exchanging error causes.
type callbackErrors struct {
	mu  sync.Mutex
	err error
}
type callbackErrorsKey struct{}

func errorContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, callbackErrorsKey{}, &callbackErrors{})
}

func recordCallbackError(ctx context.Context, err error) error {
	if err != nil {
		if state, ok := ctx.Value(callbackErrorsKey{}).(*callbackErrors); ok {
			state.mu.Lock()
			state.err = errors.Join(state.err, err)
			state.mu.Unlock()
		}
	}
	return err
}

// callScope owns the native options, the cancel signal and the context handle of one call, and
// joins the signal before freeing any of them.
type callScope struct {
	ctx    context.Context
	native *Options
	value  Handle
	finish func()
}

// result is the call's error: the context's if it is done, otherwise the status joined with what
// the callbacks recorded.
func (s *callScope) result(status error) error {
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	if state, ok := s.ctx.Value(callbackErrorsKey{}).(*callbackErrors); ok {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.err != nil {
			return errors.Join(status, state.err)
		}
	}
	return status
}

func timeoutOf(ctx context.Context) uint32 {
	d := 30 * time.Second
	if until, ok := ctx.Deadline(); ok {
		d = time.Until(until)
	}
	if d < time.Millisecond {
		return 1
	}
	if d/time.Millisecond > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(d / time.Millisecond)
}

func (s *Session) scope(ctx context.Context) (*callScope, error) {
	s.mu.RLock()
	if s.host == nil {
		s.mu.RUnlock()
		return nil, plugin.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		s.mu.RUnlock()
		return nil, err
	}
	duration := s.callTimeout
	if duration == 0 {
		duration = plugin.DefaultTimeout
	}
	ctx, cancelContext := context.WithTimeout(ctx, duration)
	ctx = errorContext(ctx)
	scope := &callScope{ctx: ctx}
	scope.value = NewHandle(ctx)
	native, status := NewOptions(timeoutOf(ctx), scope.value)
	if status != stOK {
		scope.value.Delete()
		cancelContext()
		s.mu.RUnlock()
		return nil, statusError(status)
	}
	scope.native = native
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { native.Signal(); close(done) })
	scope.finish = func() {
		if !stop() {
			<-done
		}
		native.Free()
		scope.value.Delete()
		cancelContext()
		s.mu.RUnlock()
	}
	return scope, nil
}

// resourceScope is a scope for a drain: the caller's context only, with no default bound, and the
// caller's lock is held by the caller.
func resourceScope(ctx context.Context) (*callScope, error) {
	ctx = errorContext(ctx)
	scope := &callScope{ctx: ctx}
	scope.value = NewHandle(ctx)
	duration := uint32(math.MaxUint32)
	if _, ok := ctx.Deadline(); ok {
		duration = timeoutOf(ctx)
	}
	native, status := NewOptions(duration, scope.value)
	if status != stOK {
		scope.value.Delete()
		return nil, statusError(status)
	}
	scope.native = native
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { native.Signal(); close(done) })
	if ctx.Err() != nil {
		native.Signal()
	}
	scope.finish = func() {
		if !stop() {
			<-done
		}
		native.Free()
		scope.value.Delete()
	}
	return scope, nil
}

var operationName = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,255}$`)

// eventCode names how a call ended in an observer event, as the Go session names it
// (contract/observe.go).
func eventCode(err error) string {
	if err == nil {
		return "ok"
	}
	var remote *plugin.RemoteError
	if errors.As(err, &remote) && remote != nil && operationName.MatchString(remote.Code) {
		return remote.Code
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, plugin.ErrDenied):
		return "denied"
	case errors.Is(err, plugin.ErrMismatch):
		return "mismatch"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	case errors.Is(err, plugin.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, plugin.ErrClosed):
		return "closed"
	}
	return "failed"
}

// settledEventCode names how a stage ended when the call's context is done: the engine knows only
// that its cancel signal fired or its own deadline passed, and which it sees first depends on the
// platform's timers; the context knows why it ended.
func settledEventCode(ctx context.Context, code string) string {
	if code == "canceled" || code == "deadline_exceeded" {
		if err := ctx.Err(); err != nil {
			return eventCode(err)
		}
	}
	return code
}

// observeRejected reports a call refused before the engine sees it (a context that is done, a
// closed session), with the event the Go session gives such a call: no request id, and a blank
// contract or operation when the name is not a valid one.
func (s *Session) observeRejected(ctx context.Context, contract plugin.ContractRef, operation string, start time.Time, err error) {
	if s.policies == nil || s.policies.observer == nil {
		return
	}
	event := plugin.Event{Stage: "invoke", Identity: s.descriptor.Identity, Contract: contract, Operation: operation}
	if !operationName.MatchString(event.Operation) {
		event.Operation = ""
	}
	if event.Contract.Validate() != nil {
		event.Contract = plugin.ContractRef{}
	}
	event.Duration, event.Code = time.Since(start), eventCode(err)
	s.policies.observer(ctx, event)
}

// Call implements author.Caller using engine-generated identity, surface, deadlines and
// correlation IDs. Public domain errors retain their structured details.
func (s *Session) Call(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	start := time.Now()
	// A context that is already done is refused before anything else, as the Go session does.
	if err := ctx.Err(); err != nil {
		s.observeRejected(ctx, contract, operation, start, err)
		return nil, err
	}
	data, err := json.Marshal(struct {
		Contract  plugin.ContractRef `json:"contract"`
		Operation string             `json:"operation"`
		Payload   json.RawMessage    `json:"payload,omitempty"`
	}{contract, operation, payload})
	if err != nil {
		return nil, err
	}
	scope, err := s.scope(ctx)
	if err != nil {
		s.observeRejected(ctx, contract, operation, start, err)
		return nil, err
	}
	defer scope.finish()
	out, status := s.host.Call(data, scope.native)
	if scope.ctx.Err() != nil {
		return nil, scope.ctx.Err()
	}
	if err = scope.result(statusError(status)); err != nil {
		return nil, err
	}
	var response plugin.Response
	if err = plugin.Decode(out, &response); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, response.Error
	}
	return response.Payload, nil
}

func (s *Session) start(ctx context.Context) error {
	scope, err := s.scope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	err = scope.result(statusError(s.host.Start(scope.native)))
	if scope.ctx.Err() != nil {
		return scope.ctx.Err()
	}
	return err
}

// State reports the engine's lifecycle state.
func (s *Session) State() plugin.State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.host == nil {
		return plugin.StateClosed
	}
	switch s.host.State() {
	case StateReady:
		return plugin.StateReady
	case StateDraining:
		return plugin.StateDraining
	case StateFailed:
		return plugin.StateFailed
	case StateCreated:
		return plugin.State("created")
	default:
		return plugin.StateClosed
	}
}

// DrainContext rejects new work and drains admitted calls. Cancellation reopens admission; Close is
// the separate abort operation.
func (s *Session) DrainContext(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.host == nil {
		return plugin.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scope, err := resourceScope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	err = statusError(s.host.Drain(scope.native))
	if scope.ctx.Err() != nil {
		return scope.ctx.Err()
	}
	return err
}

func (s *Session) closeHost() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.host != nil {
		s.host.Close()
	}
	if s.backend != nil {
		<-s.backend.closeDone
		s.backend.mu.Lock()
		defer s.backend.mu.Unlock()
		return s.backend.closeErr
	}
	return nil
}

func (s *Session) destroy() {
	_ = s.closeHost()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != nil {
		s.host.Destroy()
		s.host = nil
	}
}

// Close drains, then closes the backend and frees the session.
func (s *Session) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.State() == plugin.StateClosed {
		s.finish()
		return s.closeHost()
	}
	if err := s.DrainContext(ctx); err != nil {
		return err
	}
	err := s.closeHost()
	s.destroy()
	s.finish()
	return err
}

// Abort stops work, closes the backend and frees the session.
func (s *Session) Abort() error {
	err := s.closeHost()
	s.destroy()
	s.finish()
	return err
}

// Stderr returns the retained, bounded and possibly truncated stderr of a session opened with
// OpenProcess. Treat it as untrusted text.
func (s *Session) Stderr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.host == nil {
		return ""
	}
	return s.host.Stderr()
}

// backendBinding connects a plugin.Backend to the engine as its extension backend.
type backendBinding struct {
	mu         sync.Mutex
	connect    func(context.Context, plugin.Descriptor) (plugin.Backend, error)
	descriptor plugin.Descriptor
	ctx        context.Context
	cancel     context.CancelFunc
	backend    plugin.Backend
	closed     bool
	closeDone  chan struct{}
	closeErr   error
}

func backendStatus(err error) int32 {
	switch {
	case err == nil:
		return stOK
	case errors.Is(err, context.DeadlineExceeded):
		return stTimeout
	case errors.Is(err, context.Canceled):
		return stCanceled
	case errors.Is(err, plugin.ErrProtocol):
		return stProtocol
	case errors.Is(err, plugin.ErrClosed):
		return stClosed
	case errors.Is(err, plugin.ErrInvalid):
		return stInvalid
	case errors.Is(err, plugin.ErrMismatch):
		return stMismatch
	case errors.Is(err, plugin.ErrDenied):
		return stDenied
	case errors.Is(err, plugin.ErrUnsupported):
		return stUnsupported
	case errors.Is(err, plugin.ErrDraining):
		return stDraining
	default:
		return stIO
	}
}

func emitJSON(value any, emit Emit) int32 {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || len(data) > plugin.MaxFrameBytes {
		return stInvalid
	}
	return emit(data)
}

// bound is the context of a backend call: the caller's, bounded by the engine's time and ended by
// the binding's lifetime.
func (b *backendBinding) bound(caller Handle, duration time.Duration) (context.Context, func()) {
	parent := b.ctx
	if caller != 0 {
		if c, ok := caller.Value().(context.Context); ok {
			parent = c
		}
	}
	ctx, cancel := context.WithTimeout(parent, duration)
	stop := context.AfterFunc(b.ctx, cancel)
	if b.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func (b *backendBinding) Connect(caller Handle, timeoutMs uint32, emit Emit) (result int32) {
	result = stIO
	defer recovered(stIO, &result)
	ctx, cancel := b.bound(caller, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	backend, err := b.connect(ctx, b.descriptor.Clone())
	if err != nil {
		if backend != nil {
			_ = backend.Close()
		}
		return backendStatus(recordCallbackError(ctx, err))
	}
	if backend == nil {
		return stInvalid
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = backend.Close()
		return stClosed
	}
	b.backend = backend
	b.mu.Unlock()
	actual, err := backend.Handshake(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	return emitJSON(actual, emit)
}

func (b *backendBinding) Invoke(caller Handle, data []byte, timeoutMs uint32, emit Emit) (result int32) {
	result = stIO
	defer recovered(stIO, &result)
	var request plugin.Request
	if len(data) > plugin.MaxFrameBytes {
		return stInvalid
	}
	if err := plugin.Decode(data, &request); err != nil {
		return stInvalid
	}
	ctx, cancel := b.bound(caller, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	ctx, deadlineCancel := context.WithDeadline(ctx, request.Deadline)
	defer deadlineCancel()
	if err := ctx.Err(); err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	b.mu.Lock()
	backend, isClosed := b.backend, b.closed
	b.mu.Unlock()
	if backend == nil || isClosed {
		return stClosed
	}
	response, err := backend.Invoke(ctx, request)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	return emitJSON(response, emit)
}

func (b *backendBinding) Close() {
	defer close(b.closeDone)
	defer func() {
		if recover() != nil {
			b.mu.Lock()
			b.closeErr = errors.New("backend close failed")
			b.mu.Unlock()
		}
	}()
	b.cancel()
	b.mu.Lock()
	b.closed = true
	backend := b.backend
	b.mu.Unlock()
	if backend != nil {
		err := backend.Close()
		b.mu.Lock()
		b.closeErr = err
		b.mu.Unlock()
	}
}

// Release runs once the host is destroyed; the binding holds nothing native.
func (b *backendBinding) Release() {}

func allow([]byte) bool { return true }

// newSession creates the host for Open and OpenProcess. backend is nil for a process the engine
// owns.
func newSession(d plugin.Descriptor, connect func(context.Context, plugin.Descriptor) (plugin.Backend, error), launch *LaunchOptions) (*Session, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	s := &Session{descriptor: d.Clone(), done: make(chan struct{}), policies: &policies{}}
	var status int32
	if launch != nil {
		s.host, status = NewLaunchHost(*launch, data, allow, allow)
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		s.backend = &backendBinding{connect: connect, descriptor: d.Clone(), ctx: ctx, cancel: cancel, closeDone: make(chan struct{})}
		s.host, status = NewBackendHost(data, BackendConcurrent, s.backend, allow, allow)
		if status != stOK {
			cancel()
		}
	}
	if status != stOK {
		return nil, statusError(status)
	}
	return s, nil
}

// Open runs a plugin over a Go backend from options.Connect. The engine owns the session. A plugin
// that declares progress or reverse operations is refused, and options.Reverse is unsupported.
func Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (*Session, error) {
	if options.Verify == nil || options.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if options.Connect == nil || options.Timeout < 0 || options.Timeout/time.Millisecond > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	if options.Reverse != nil {
		return nil, plugin.ErrUnsupported
	}
	return openSession(ctx, d, options, func() (*Session, error) { return newSession(d, options.Connect, nil) })
}

// OpenProcess is Open with the child process owned by the engine: it starts the launch, then
// handshakes. options.Connect is unused.
func OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	if options.Verify == nil || options.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if options.Timeout < 0 || options.Timeout/time.Millisecond > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	if options.Reverse != nil {
		return nil, plugin.ErrUnsupported
	}
	if launch.StderrBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	return openSession(ctx, d, options, func() (*Session, error) { return newSession(d, nil, &launch) })
}

func openSession(ctx context.Context, d plugin.Descriptor, options plugin.Options, create func() (*Session, error)) (*Session, error) {
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
	if _, err = Call("descriptor.requirements", data); err != nil {
		return nil, err
	}
	s, err := create()
	if err != nil {
		return nil, err
	}
	s.callTimeout = options.Timeout
	s.policies.verifyContext = func(ctx context.Context, raw []byte) error {
		var d plugin.Descriptor
		if err := plugin.Decode(raw, &d); err != nil {
			return err
		}
		return options.Verify(ctx, d)
	}
	s.policies.authorizeContext = func(ctx context.Context, raw []byte) error {
		var r plugin.Request
		if err := plugin.Decode(raw, &r); err != nil {
			return err
		}
		return options.Authorize(ctx, r)
	}
	s.policies.observer = options.Observer
	if status := s.host.SetHooks(s.hooks()); status != stOK {
		s.destroy()
		return nil, statusError(status)
	}
	if err = s.start(ctx); err != nil {
		s.destroy()
		return nil, err
	}
	return s, nil
}

// hooks are the engine's contextual policies and observer, in terms of Go contexts.
func (s *Session) hooks() Hooks {
	p := s.policies
	contextOf := func(call Handle) context.Context {
		if call != 0 {
			if c, ok := call.Value().(context.Context); ok {
				return c
			}
		}
		return context.Background()
	}
	return Hooks{
		Verify: func(call Handle, data []byte) bool {
			ctx := contextOf(call)
			if ctx.Err() != nil {
				return false
			}
			err := recordCallbackError(ctx, p.verifyContext(ctx, data))
			p.verifyMu.Lock()
			p.verifyErr = err
			p.verifyMu.Unlock()
			return err == nil
		},
		Authorize: func(call Handle, data []byte) bool {
			ctx := contextOf(call)
			if ctx.Err() != nil {
				return false
			}
			return recordCallbackError(ctx, p.authorizeContext(ctx, data)) == nil
		},
		Observe: func(call Handle, data []byte) {
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
			if plugin.Decode(data, &event) != nil {
				return
			}
			if event.Stage == "verify" && event.Code == "denied" {
				// The Go session reports the verify stage with the policy's own error, which is
				// "failed" for an error that does not say it denied.
				p.verifyMu.Lock()
				if p.verifyErr != nil {
					event.Code = eventCode(p.verifyErr)
				}
				p.verifyMu.Unlock()
			}
			ctx := contextOf(call)
			event.Code = settledEventCode(ctx, event.Code)
			p.observer(ctx, plugin.Event{Stage: event.Stage, Identity: event.Identity, Contract: event.Contract, Operation: event.Operation, RequestID: event.RequestID, Code: event.Code, Duration: time.Duration(event.Duration) * time.Millisecond})
		},
	}
}
