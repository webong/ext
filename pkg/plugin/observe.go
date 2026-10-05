package plugin

import (
	"context"
	"errors"
	"time"
)

// Event contains bounded metadata, never request/response payloads or error
// messages. Observers can derive logs, counters, latency histograms and spans.
type Event struct {
	Stage     string
	Identity  Identity
	Contract  ContractRef
	Operation string
	RequestID string
	Duration  time.Duration
	Code      string
}

// Observer is synchronous and must be fast, concurrency-safe and nonblocking.
// ctx permits integration with the host's own tracing system. Observers must
// not panic. No global logger, exporter, payload capture or telemetry is used.
type Observer func(context.Context, Event)

func eventCode(err error) string {
	if err == nil {
		return "ok"
	}
	var remote *RemoteError
	if errors.As(err, &remote) && remote != nil && identifier.MatchString(remote.Code) {
		return remote.Code
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrDenied):
		return "denied"
	case errors.Is(err, ErrMismatch):
		return "mismatch"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrClosed):
		return "closed"
	default:
		return "failed"
	}
}

func observe(ctx context.Context, observer Observer, event Event, start time.Time, err error) {
	if observer != nil {
		if !identifier.MatchString(event.Operation) {
			event.Operation = ""
		}
		if event.Contract.Validate() != nil {
			event.Contract = ContractRef{}
		}
		event.Duration, event.Code = time.Since(start), eventCode(err)
		observer(ctx, event)
	}
}
