// Package bridge exposes a verified CTX backend as a CTX endpoint. It changes
// transport, not domain meaning. Both sides retain their own admission policy.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/webong/ext/pkg/plugin"
)

// Relay owns one upstream Session. Incoming calls keep their external ID; the
// upstream Session assigns independent IDs. No retries or authority forwarding
// are implicit. A bridge's upstream credentials belong to its configured policy.
type Session interface {
	State() plugin.State
	Descriptor() plugin.Descriptor
	Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)
	Close(context.Context) error
	Abort() error
}

type Relay struct{ session Session }

// New takes ownership of a ready, policy-enforcing session. This permits engine
// bindings to provide shared mechanics without nesting a second Go Session.
func New(session Session) (*Relay, error) {
	if session == nil {
		return nil, plugin.ErrInvalid
	}
	value := reflect.ValueOf(session)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, plugin.ErrInvalid
	}
	if session.State() != plugin.StateReady {
		return nil, plugin.ErrClosed
	}
	if err := session.Descriptor().Validate(); err != nil {
		return nil, err
	}
	return &Relay{session: session}, nil
}

func Open(ctx context.Context, selected plugin.Descriptor, options plugin.Options) (*Relay, error) {
	s, err := plugin.Open(ctx, selected, options)
	if err != nil {
		return nil, err
	}
	return New(s)
}
func (r *Relay) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return plugin.Descriptor{}, err
	}
	if r.session.State() != plugin.StateReady {
		return plugin.Descriptor{}, plugin.ErrClosed
	}
	return r.session.Descriptor(), nil
}
func (r *Relay) Invoke(ctx context.Context, request plugin.Request) (plugin.Response, error) {
	response := plugin.Response{APIVersion: plugin.APIVersion, ID: request.ID}
	if err := plugin.ValidateRequest(r.session.Descriptor(), request); err != nil {
		response.Error = &plugin.RemoteError{Code: "invalid_request", Message: "request does not match selected contract"}
		return response, response.Validate(request.ID)
	}
	ctx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	payload, err := r.session.Call(ctx, request.Contract, request.Operation, request.Payload)
	switch {
	case err == nil:
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		response.Payload = payload
	case r.session.State() == plugin.StateFailed || r.session.State() == plugin.StateClosed:
		// A transport failure cannot become a domain error and leave the outer host
		// believing its connection is still healthy. The frontend closes on error.
		return plugin.Response{}, err
	case errors.Is(err, plugin.ErrDenied):
		response.Error = &plugin.RemoteError{Code: "permission_denied", Message: "bridge operation denied"}
	default:
		var remote *plugin.RemoteError
		if errors.As(err, &remote) && remote != nil {
			copy := *remote
			response.Error = &copy
		} else {
			return plugin.Response{}, err
		}
	}
	return response, response.Validate(request.ID)
}

// Close aborts upstream I/O and releases the backend exactly once. The serving
// frontend must call it on EOF, transport error and cancellation.
func (r *Relay) Close() error { return r.session.Abort() }

// Drain stops admission and waits for admitted calls, using Session semantics.
func (r *Relay) Drain(ctx context.Context) error { return r.session.Close(ctx) }

var _ plugin.Backend = (*Relay)(nil)
