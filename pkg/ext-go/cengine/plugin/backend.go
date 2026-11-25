package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

func (s *Session) connect(json.RawMessage) (any, error) {
	b, err := s.options.Connect(s.start, s.descriptor.Clone())
	valid := b != nil
	if valid {
		v := reflect.ValueOf(b)
		if v.Kind() == reflect.Pointer && v.IsNil() {
			valid = false
		}
	}
	if err != nil {
		if valid {
			_ = b.Close()
		}
		return nil, backendError(err)
	}
	if !valid {
		return nil, engine.ErrInvalid
	}
	s.mu.Lock()
	if s.backendClosed {
		s.mu.Unlock()
		_ = b.Close()
		return nil, engine.ErrClosed
	}
	s.backend = b
	s.mu.Unlock()
	if reverse, ok := b.(engine.ReverseBackend); ok {
		reverse.SetReverse(s)
	}
	descriptor, err := b.Handshake(s.start)
	return descriptor, backendError(err)
}
func (s *Session) closeBackend() {
	s.mu.Lock()
	s.backendClosed = true
	b := s.backend
	s.backend = nil
	s.mu.Unlock()
	if b != nil {
		_ = b.Close()
	}
}
func (s *Session) invoke(data json.RawMessage) (any, error) {
	response, err := s.invokeIn(s.life, data)
	return response, backendError(err)
}
func (s *Session) invokeIn(ctx context.Context, data json.RawMessage) (any, error) {
	var r engine.Request
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithDeadline(ctx, r.Deadline)
	defer cancel()
	if r.Progress != nil {
		ctx = engine.WithProgressSink(ctx, progressSink{s: s, parent: r.ID})
	}
	if s.options.Reverse != nil {
		ctx = engine.WithReverse(ctx, reverseCaller{s: s, parent: r.ID})
		ctx = engine.WithSessionReverse(ctx, reverseCaller{s: s})
	}
	s.mu.Lock()
	b := s.backend
	s.mu.Unlock()
	if b == nil {
		return nil, engine.ErrClosed
	}
	response, err := b.Invoke(ctx, r)
	return response, err
}

type progressSink struct {
	s      *Session
	parent string
}

func (p progressSink) Deliver(event engine.Progress) (bool, error) {
	var delivered bool
	err := p.s.host.Call("backend_progress", map[string]any{"frame": engine.ProgressFrame{Frame: engine.FrameProgress, APIVersion: engine.APIVersion, Parent: p.parent, Progress: event}}, &delivered)
	return delivered, engineError(err)
}
func (s *Session) AdmitReverse(frame engine.ReverseRequestFrame) (func() engine.ReverseResult, *engine.ReverseResult, error) {
	return func() engine.ReverseResult {
		var response engine.ReverseResponseFrame
		err := s.host.Call("backend_reverse", map[string]any{"frame": frame}, &response)
		if err != nil {
			return engine.ReverseResult{Error: &engine.RemoteError{Code: "operation_failed", Message: "reverse call failed"}}
		}
		return engine.ReverseResult{Payload: response.Payload, Error: response.Error}
	}, nil, nil
}

var reverseSequence atomic.Uint64

type reverseCaller struct {
	s      *Session
	parent string
}

func (r reverseCaller) Call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(engine.DefaultTimeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	frame := engine.ReverseRequestFrame{Frame: engine.FrameReverseRequest, APIVersion: engine.APIVersion, ID: fmt.Sprintf("reverse-%d", reverseSequence.Add(1)), Parent: r.parent, Contract: contract, Operation: operation, Payload: payload, Deadline: deadline}
	run, _, err := r.s.AdmitReverse(frame)
	if err != nil {
		return nil, err
	}
	result := run()
	if result.Error != nil {
		return nil, result.Error
	}
	return result.Payload, nil
}

func backendError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &engine.RemoteError{Code: "timeout", Message: "backend deadline exceeded"}
	}
	if errors.Is(err, context.Canceled) {
		return &engine.RemoteError{Code: "canceled", Message: "backend canceled"}
	}
	if errors.Is(err, engine.ErrUnsupported) {
		return &engine.RemoteError{Code: "unsupported", Message: "backend unsupported"}
	}
	return err
}
