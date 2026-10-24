package engine

import (
	"context"
	"encoding/json"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// inner is what the three engines' own session types have in common.
type inner interface {
	Descriptor() plugin.Descriptor
	State() plugin.State
	Done() <-chan struct{}
	Call(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error)
	Close(ctx context.Context) error
	Abort() error
	Stderr() string
}

// progressCaller is the session of an engine that carries progress events.
type progressCaller interface {
	CallProgress(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage, onProgress func(plugin.Progress), options ...plugin.CallOptions) (json.RawMessage, error)
}

// session is an engine's own session with the name of the engine on it.
type session struct {
	name Name
	inner
}

func (s session) Engine() Name { return s.name }

func (s session) CallProgress(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage, onProgress func(plugin.Progress), options ...plugin.CallOptions) (json.RawMessage, error) {
	if p, ok := s.inner.(progressCaller); ok {
		return p.CallProgress(ctx, contract, operation, payload, onProgress, options...)
	}
	return nil, errUnsupportedBy(s.name, "progress events")
}
