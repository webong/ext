package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// reverseCaller lets code in the host's process (an in-process plugin) call the
// host's reverse services directly. parent is the forward call it belongs to, or
// empty for session scope.
type reverseCaller struct {
	s      *Session
	parent string
}

func (c reverseCaller) Call(ctx context.Context, ref ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.s.reverseTimeout())
	}
	s := c.s
	s.mu.Lock()
	n := s.nextReverse()
	s.mu.Unlock()
	frame := ReverseRequestFrame{Frame: FrameReverseRequest, APIVersion: APIVersion, ID: n, Parent: c.parent, Contract: ref, Operation: operation, Deadline: deadline, Payload: payload}
	if err := frame.Validate(); err != nil {
		return nil, ErrInvalid
	}
	run, immediate, err := s.admitReverse(frame, ctx)
	if err != nil {
		return nil, err
	}
	var result ReverseResult
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

// nextReverse returns a fresh identifier for a reverse call made in-process.
// The caller holds s.mu.
func (s *Session) nextReverse() string {
	s.nextRev++
	return fmt.Sprintf("r%d", s.nextRev)
}

func (s *Session) reverseTimeout() time.Duration {
	if s.reverse != nil && s.reverse.Timeout > 0 {
		return s.reverse.Timeout
	}
	return DefaultTimeout
}

func (s *Session) reverseLimits() (payload, inFlight int) {
	payload, inFlight = DefaultReversePayloadBytes, DefaultReverseInFlight
	if s.reverse != nil {
		if s.reverse.MaxPayloadBytes > 0 {
			payload = s.reverse.MaxPayloadBytes
		}
		if s.reverse.MaxInFlight > 0 {
			inFlight = s.reverse.MaxInFlight
		}
	}
	return payload, inFlight
}

func denied(code string) *ReverseResult {
	return &ReverseResult{Error: &RemoteError{Code: code, Message: "reverse call refused"}}
}

// AdmitReverse implements ReverseHost for a backend that carries frames.
func (s *Session) AdmitReverse(frame ReverseRequestFrame) (func() ReverseResult, *ReverseResult, error) {
	if err := frame.Validate(); err != nil {
		return nil, nil, err
	}
	return s.admitReverse(frame, nil)
}

// admitReverse checks one reverse request. caller is the calling plugin's own
// context for an in-process call (nil for a wire call); it only ends the
// handler early. The handler's context always derives from the parent call (or
// the session), never from the plugin's, whose values include this session's own
// reverse caller: a nested host must find its upstream there.
func (s *Session) admitReverse(frame ReverseRequestFrame, caller context.Context) (func() ReverseResult, *ReverseResult, error) {
	s.mu.Lock()
	ready, state := s.ready, s.state
	parentCtx, parentLive := s.calls[frame.Parent]
	s.mu.Unlock()
	if frame.Parent != "" && !parentLive {
		return nil, nil, fmt.Errorf("%w: reverse call names a call that is not in flight", ErrProtocol)
	}
	if _, err := s.descriptor.LookupReverse(frame.Contract, frame.Operation); err != nil {
		return nil, denied(ReverseUndeclared), nil
	}
	if s.reverse == nil || !ready || state != StateReady && state != StateDraining {
		return nil, denied(ReverseUnavailable), nil
	}
	maxPayload, maxInFlight := s.reverseLimits()
	if len(frame.Payload) > maxPayload {
		return nil, denied(ReverseTooLarge), nil
	}
	if !time.Now().Before(frame.Deadline) {
		return nil, denied(ReverseExpired), nil
	}
	s.mu.Lock()
	if s.reverses >= maxInFlight {
		s.mu.Unlock()
		return nil, denied(ReverseBusy), nil
	}
	s.reverses++
	s.mu.Unlock()
	ctx := s.base
	if frame.Parent != "" {
		ctx = parentCtx
	}
	return func() ReverseResult {
		defer func() { s.mu.Lock(); s.reverses--; s.mu.Unlock() }()
		return s.runReverse(ctx, caller, frame, maxPayload)
	}, nil, nil
}

func (s *Session) runReverse(ctx, caller context.Context, frame ReverseRequestFrame, maxPayload int) ReverseResult {
	ctx, cancel := context.WithTimeout(ctx, s.reverseTimeout())
	defer cancel()
	if caller != nil {
		defer context.AfterFunc(caller, cancel)()
	}
	ctx, cancelDeadline := context.WithDeadline(ctx, frame.Deadline)
	defer cancelDeadline()
	stop := context.AfterFunc(s.base, cancel)
	defer stop()
	op, _ := s.descriptor.LookupReverse(frame.Contract, frame.Operation)
	request := Request{APIVersion: APIVersion, ID: frame.ID, Plugin: s.descriptor.Identity, Contract: frame.Contract, Operation: frame.Operation, Surface: op.Surface, Deadline: frame.Deadline, Payload: append(json.RawMessage(nil), frame.Payload...), Parent: frame.Parent}
	if err := s.reverse.Authorize(ctx, request.Clone()); err != nil {
		return *denied(ReverseDenied)
	}
	if err := ctx.Err(); err != nil {
		return *denied(ReverseExpired)
	}
	payload, err := s.reverse.Handle(ctx, request.Clone())
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) && remote != nil {
			return ReverseResult{Error: publicError(remote, "host service failed")}
		}
		return ReverseResult{Error: &RemoteError{Code: "operation_failed", Message: "host service failed"}}
	}
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	if len(payload) > maxPayload {
		return *denied(ReverseTooLarge)
	}
	if err := (Response{APIVersion: APIVersion, ID: frame.ID, Payload: payload}).Validate(frame.ID); err != nil {
		return ReverseResult{Error: &RemoteError{Code: "operation_failed", Message: "host service failed"}}
	}
	return ReverseResult{Payload: append(json.RawMessage(nil), payload...)}
}
