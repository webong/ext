package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Endpoint is the shared host/guest interface. Transport clients implement it
// for hosts; Guest implements it for plugin authors. The interface carries no
// transport or process ownership. Backend adds host-side connection cleanup.
type Endpoint interface {
	Handshake(context.Context) (Descriptor, error)
	Invoke(context.Context, Request) (Response, error)
}

// Handler authorizes and executes a consumer-owned operation. Implementations
// must check domain authority, honor ctx, and support concurrent calls when
// used with concurrent transports. Return RemoteError only for intentional
// public failures; other errors are reduced to generic diagnostic text.
type Handler func(context.Context, Request) (json.RawMessage, error)

type GuestOptions struct {
	Handler Handler
	// MaxCallDuration is a guest-owned bound, independently of host deadlines.
	// Zero defaults to DefaultTimeout.
	MaxCallDuration time.Duration
}

// Guest holds an immutable declaration and a domain handler, reusable across
// in-process, JSON-line, RPC, native Go, WASI and C ABI backends. Each backend owns its
// connections; shutting one connection does not shut down the guest process.
type Guest struct {
	descriptor Descriptor
	handler    Handler
	timeout    time.Duration
}

func NewGuest(descriptor Descriptor, opts GuestOptions) (*Guest, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	if opts.Handler == nil {
		return nil, ErrDenied
	}
	if opts.MaxCallDuration < 0 {
		return nil, ErrInvalid
	}
	if opts.MaxCallDuration == 0 {
		opts.MaxCallDuration = DefaultTimeout
	}
	return &Guest{descriptor: descriptor.Clone(), handler: opts.Handler, timeout: opts.MaxCallDuration}, nil
}

func (g *Guest) Handshake(ctx context.Context) (Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	return g.descriptor.Clone(), nil
}

func (g *Guest) Invoke(ctx context.Context, request Request) (Response, error) {
	response := Response{APIVersion: APIVersion, ID: request.ID}
	if err := ValidateRequest(g.descriptor, request); err != nil {
		response.Error = &RemoteError{Code: "invalid_request", Message: "request does not match selected contract"}
		return response, response.Validate(request.ID)
	}
	ctx, deadlineCancel := context.WithDeadline(ctx, request.Deadline)
	defer deadlineCancel()
	ctx, limitCancel := context.WithTimeout(ctx, g.timeout)
	defer limitCancel()
	var err error
	if ctx.Err() != nil {
		err = ctx.Err()
	} else {
		response.Payload, err = g.handler(ctx, request.Clone())
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		response.Payload = nil
		var remote *RemoteError
		if errors.As(err, &remote) && remote != nil {
			copy := *remote
			response.Error = &copy
		} else {
			response.Error = &RemoteError{Code: "operation_failed", Message: "plugin operation failed"}
		}
	} else if len(response.Payload) == 0 {
		response.Payload = json.RawMessage("null")
	}
	response.Payload = append(json.RawMessage(nil), response.Payload...)
	return response, response.Validate(request.ID)
}
