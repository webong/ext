package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Backend is a consumer-selected transport binding. Implementations must honor
// cancellation, permit concurrent Invoke calls (possibly serialized internally),
// and make Close interrupt transport I/O. In-host native execution has no forced
// termination: native Go handlers cooperate with contexts; C ABI cancellation
// releases the caller while native work and cleanup may remain outstanding.
// Backends never decide product authorization. A CLI binding may preserve raw
// streams outside this JSON invocation API and use ValidateRequest directly.
type Backend interface {
	Endpoint
	Close() error
}

type Options struct {
	// Verify must check the reviewed package selection before Connect may run.
	// A digest proves content equality, not publisher trust or permission.
	Verify  func(context.Context, Descriptor) error
	Connect func(context.Context, Descriptor) (Backend, error)
	// Authorize is required, called on every invocation, and receives copies.
	Authorize func(context.Context, Request) error
	// Timeout bounds handshake and calls without an earlier caller deadline.
	Timeout time.Duration
	// Observer receives metadata for admission and completed calls.
	Observer Observer
	// Requirements are checked before verification or connection.
	Requirements []Requirement
}

type State string

const (
	StateReady    State = "ready"
	StateDraining State = "draining"
	StateClosed   State = "closed"
	StateFailed   State = "failed"
)

// Session owns invocation admission and draining. The chosen backend or host
// owns process lifecycle, using pkg/graph/supervisor or the backend's runtime (such as
// go-plugin). A process restart requires a fresh Session and handshake.
type Session struct {
	descriptor Descriptor
	backend    Backend
	authorize  func(context.Context, Request) error
	timeout    time.Duration
	observer   Observer
	mu         sync.Mutex
	state      State
	next       uint64
	active     int
	idle       chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

// Open verifies selection before opening a backend, then checks its exact
// handshake. Nil policy callbacks fail closed. No global registry is used.
func Open(ctx context.Context, selected Descriptor, opts Options) (*Session, error) {
	selected = selected.Clone()
	if err := CheckRequirements(selected, opts.Requirements); err != nil {
		return nil, err
	}
	if opts.Verify == nil || opts.Authorize == nil {
		return nil, ErrDenied
	}
	if opts.Connect == nil || opts.Timeout < 0 {
		return nil, ErrInvalid
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start := time.Now()
	verifyErr := opts.Verify(ctx, selected.Clone())
	observe(ctx, opts.Observer, Event{Stage: "verify", Identity: selected.Identity}, start, verifyErr)
	if err := verifyErr; err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDenied, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start = time.Now()
	backend, err := opts.Connect(ctx, selected.Clone())
	observe(ctx, opts.Observer, Event{Stage: "connect", Identity: selected.Identity}, start, err)
	if err != nil {
		if backend != nil {
			_ = backend.Close()
		}
		return nil, err
	}
	if backend == nil {
		return nil, ErrInvalid
	}
	start = time.Now()
	actual, err := backend.Handshake(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = MatchHandshake(selected, actual)
	}
	observe(ctx, opts.Observer, Event{Stage: "handshake", Identity: selected.Identity}, start, err)
	if err != nil {
		return nil, errors.Join(err, backend.Close())
	}
	idle := make(chan struct{})
	close(idle)
	return &Session{descriptor: selected, backend: backend, authorize: opts.Authorize, timeout: opts.Timeout, observer: opts.Observer, state: StateReady, idle: idle}, nil
}

func (s *Session) Descriptor() Descriptor { return s.descriptor.Clone() }
func (s *Session) State() State           { s.mu.Lock(); defer s.mu.Unlock(); return s.state }

// Call derives identity, surface, deadline, and a fresh correlation ID from
// host state. A payload cannot expand the selected descriptor. Request IDs do
// not supply idempotency, authorization, or automatic retries.
func (s *Session) Call(ctx context.Context, contract ContractRef, operation string, payload json.RawMessage) (result json.RawMessage, callErr error) {
	start := time.Now()
	event := Event{Stage: "invoke", Identity: s.descriptor.Identity, Contract: contract, Operation: operation}
	defer func() { observe(ctx, s.observer, event, start, callErr) }()
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	op, err := s.descriptor.Lookup(contract, operation)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.state != StateReady {
		err := ErrClosed
		if s.state == StateDraining {
			err = ErrDraining
		}
		s.mu.Unlock()
		return nil, err
	}
	if s.active == 0 {
		s.idle = make(chan struct{})
	}
	s.active++
	s.next++
	id := strconv.FormatUint(s.next, 10)
	event.RequestID = id
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		if s.active == 0 {
			close(s.idle)
		}
		s.mu.Unlock()
	}()
	deadline, _ := ctx.Deadline()
	request := Request{APIVersion: APIVersion, ID: id, Plugin: s.descriptor.Identity, Contract: contract, Operation: operation, Surface: op.Surface, Deadline: deadline, Payload: append(json.RawMessage(nil), payload...)}
	if err := ValidateRequest(s.descriptor, request); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, request.Clone()); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDenied, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := s.backend.Invoke(ctx, request)
	if err == nil {
		err = ctx.Err()
	}
	// A guest's deadline timer may fire before the host context's timer is
	// scheduled. Do not accept a late response merely because Err is still nil.
	if err == nil && !time.Now().Before(deadline) {
		err = context.DeadlineExceeded
	}
	if err == nil {
		err = response.Validate(id)
	}
	if err != nil {
		s.fail()
		return nil, err
	}
	if response.Error != nil {
		e := *response.Error
		return nil, &e
	}
	return append(json.RawMessage(nil), response.Payload...), nil
}

// Close first rejects new invocations, then drains admitted work. A deadline
// while draining reopens admission without closing the backend. Concurrent
// Close attempts receive ErrDraining. An already closed session is idempotent.
func (s *Session) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	switch s.state {
	case StateClosed, StateFailed:
		s.mu.Unlock()
		return s.closeBackend()
	case StateDraining:
		s.mu.Unlock()
		return ErrDraining
	}
	s.state = StateDraining
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.mu.Lock()
		if s.state == StateDraining {
			s.state = StateReady
		}
		s.mu.Unlock()
		return ctx.Err()
	case <-idle:
		s.mu.Lock()
		if s.state == StateDraining {
			s.state = StateClosed
		}
		s.mu.Unlock()
		return s.closeBackend()
	}
}

// Abort interrupts the transport immediately. The owning host must separately
// stop any process through its supervisor. It is safe alongside Call or Close.
func (s *Session) Abort() error {
	s.mu.Lock()
	s.state = StateClosed
	s.mu.Unlock()
	return s.closeBackend()
}
func (s *Session) fail() {
	s.mu.Lock()
	if s.state != StateClosed {
		s.state = StateFailed
	}
	s.mu.Unlock()
	_ = s.closeBackend()
}
func (s *Session) closeBackend() error {
	s.closeOnce.Do(func() { s.closeErr = s.backend.Close() })
	return s.closeErr
}
