package contract

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestEventCode(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, "ok"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{context.Canceled, "canceled"},
		{ErrDenied, "denied"},
		{ErrMismatch, "mismatch"},
		{ErrInvalid, "invalid"},
		{ErrUnsupported, "unsupported"},
		{ErrClosed, "closed"},
		// A call refused while the session drains is told apart from one that failed.
		{ErrDraining, "draining"},
		{fmt.Errorf("%w: wrapped", ErrDraining), "draining"},
		{&RemoteError{Code: "busy", Message: "m"}, "busy"},
		{&RemoteError{Code: "Not An Identifier", Message: "m"}, "failed"},
		{errors.New("other"), "failed"},
	} {
		if got := eventCode(c.err); got != c.want {
			t.Errorf("eventCode(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
