package enginetest

import (
	"context"
	"errors"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// Causes lists the classes an error can belong to. Two implementations agree
// when an error is in exactly the same classes, whatever its text.
func Causes(err error) string {
	if err == nil {
		return "ok"
	}
	var out string
	for _, c := range []struct {
		name string
		err  error
	}{
		{"invalid", plugin.ErrInvalid}, {"unsupported", plugin.ErrUnsupported}, {"notfound", plugin.ErrNotFound},
		{"ambiguous", plugin.ErrAmbiguous}, {"denied", plugin.ErrDenied}, {"mismatch", plugin.ErrMismatch},
		{"closed", plugin.ErrClosed}, {"draining", plugin.ErrDraining}, {"protocol", plugin.ErrProtocol},
		{"canceled", context.Canceled}, {"deadline", context.DeadlineExceeded},
	} {
		if errors.Is(err, c.err) {
			out += c.name + ","
		}
	}
	var remote *plugin.RemoteError
	if errors.As(err, &remote) {
		out += "remote:" + remote.Code + ","
	}
	if out == "" {
		out = "other"
	}
	return out
}
