package jsonline

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/webong/ext/pkg/plugin"
)

// Func handles one call for a guest that addresses operations by a single
// string, Contract.Name + "." + Operation. payload is the request payload (nil
// when absent). The result must be JSON-encodable.
type Func func(ctx context.Context, method string, payload json.RawMessage) (any, error)

// FuncOptions tunes ServeFunc and NewFuncGuest.
type FuncOptions struct {
	// RedactErrors replaces the message of an ordinary error with a generic
	// one. By default the message reaches the host (as untrusted diagnostics).
	RedactErrors bool
	// MaxCallDuration is the guest-owned bound; zero uses plugin.DefaultTimeout.
	MaxCallDuration time.Duration
}

// FailureCode is the code of the RemoteError produced for an ordinary error.
const FailureCode = "provider_error"

// MaxFailureMessageBytes bounds an ordinary error's message.
const MaxFailureMessageBytes = 4096

// NewFuncGuest adapts fn to a guest endpoint for descriptor. An error that is
// (or wraps) a *plugin.RemoteError is sent as is; any other error becomes
// RemoteError{Code: FailureCode, Message: err.Error()} cut to
// MaxFailureMessageBytes on a character boundary, or a generic message when
// RedactErrors is set.
func NewFuncGuest(descriptor plugin.Descriptor, fn Func, options FuncOptions) (*plugin.Guest, error) {
	if fn == nil {
		return nil, plugin.ErrDenied
	}
	return plugin.NewGuest(descriptor, plugin.GuestOptions{
		MaxCallDuration: options.MaxCallDuration,
		Handler: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
			result, err := fn(ctx, plugin.Method(request.Contract, request.Operation), request.Payload)
			if err != nil {
				return nil, failure(err, options.RedactErrors)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return nil, failure(err, options.RedactErrors)
			}
			return encoded, nil
		},
	})
}

// ServeFunc serves fn for descriptor on this process's stdin and stdout until
// the stream ends; see ServeStdio for the stream rules. Call once from main.
func ServeFunc(ctx context.Context, descriptor plugin.Descriptor, fn Func, options FuncOptions) error {
	guest, err := NewFuncGuest(descriptor, fn, options)
	if err != nil {
		return err
	}
	return ServeStdio(ctx, guest)
}

func failure(err error, redact bool) error {
	var remote *plugin.RemoteError
	if errors.As(err, &remote) && remote != nil {
		return remote
	}
	if redact {
		return errors.New("plugin operation failed") // reduced by the guest to operation_failed
	}
	return &plugin.RemoteError{Code: FailureCode, Message: truncate(err.Error(), MaxFailureMessageBytes)}
}

// truncate cuts s to at most max bytes without splitting a UTF-8 character.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
