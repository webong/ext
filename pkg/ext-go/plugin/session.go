package plugin

import (
	"context"
	"encoding/json"

	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// inner is what the three engines' own session types have in common.
type inner interface {
	Descriptor() engine.Descriptor
	State() engine.State
	Done() <-chan struct{}
	Call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error)
	Close(ctx context.Context) error
	Abort() error
	Stderr() string
}

// progressCaller is the session of an engine that carries progress events.
type progressCaller interface {
	CallProgress(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), options ...engine.CallOptions) (json.RawMessage, error)
}

// session is an engine's own session with the name of the engine on it.
type session struct {
	name Name
	inner
}

func (s session) Engine() Name { return s.name }

func (s session) CallProgress(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), options ...engine.CallOptions) (json.RawMessage, error) {
	if p, ok := s.inner.(progressCaller); ok {
		return p.CallProgress(ctx, contract, operation, payload, onProgress, options...)
	}
	return nil, errUnsupportedBy(s.name, "progress events")
}
