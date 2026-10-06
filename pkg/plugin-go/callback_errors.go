//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"errors"
	"sync"
)

// Errors stay within the Go caller's scope; only bounded status codes cross C.
// A per-call recorder keeps concurrent invocations from exchanging error causes.
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
