// Package adapterkit implements the portable process framing for credential
// store adapters. Native lookup and write policy remain in each store adapter.
package adapterkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxValueBytes = 1024 * 1024

// Store is implemented by an adapter that owns one native credential store.
type Store interface {
	Check(context.Context) error
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte, bool) error
}

// Invocation is the minimal, host-independent operation delivered to a
// credential store. The adapter executable parses its own process envelope.
type Invocation struct {
	Operation string
	Selection string
	Arguments []string
}

// Run executes one CTX adapter protocol request. Values flow only through
// stdin/stdout, never through command arguments or environment variables.
func Run(request Invocation, input io.Reader, output, diagnostics io.Writer, store Store) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch request.Operation {
	case "validate", "doctor":
		if request.Selection != "" {
			fmt.Fprintln(diagnostics, "ctx: credential store has no selectable context")
			return 2
		}
		if err := store.Check(ctx); err != nil {
			fmt.Fprintf(diagnostics, "ctx: credential store unavailable: %v\n", err)
			return 1
		}
		return 0
	case "share":
		if request.Selection != "" || len(request.Arguments) < 3 || request.Arguments[0] != "credential" {
			fmt.Fprintln(diagnostics, "ctx: expected credential get|put <item>")
			return 2
		}
		item := request.Arguments[2]
		if item == "" || len(item) > 2048 || strings.ContainsAny(item, "\x00\r\n") {
			fmt.Fprintln(diagnostics, "ctx: invalid credential item")
			return 2
		}
		switch request.Arguments[1] {
		case "get":
			if len(request.Arguments) != 3 {
				return 2
			}
			value, err := store.Get(ctx, item)
			if err != nil {
				fmt.Fprintf(diagnostics, "ctx: credential read failed: %v\n", err)
				return 1
			}
			if len(value) == 0 || len(value) > maxValueBytes {
				fmt.Fprintln(diagnostics, "ctx: credential value must contain 1 byte to 1 MiB")
				return 1
			}
			defer erase(value)
			if _, err := output.Write(value); err != nil {
				fmt.Fprintf(diagnostics, "ctx: credential output failed: %v\n", err)
				return 1
			}
			return 0
		case "put":
			replace := len(request.Arguments) == 4 && request.Arguments[3] == "--replace"
			if len(request.Arguments) != 3 && !replace {
				return 2
			}
			value, err := io.ReadAll(io.LimitReader(input, maxValueBytes+1))
			if err != nil || len(value) == 0 || len(value) > maxValueBytes {
				fmt.Fprintln(diagnostics, "ctx: credential input must contain 1 byte to 1 MiB")
				return 2
			}
			defer erase(value)
			if err := store.Put(ctx, item, value, replace); err != nil {
				fmt.Fprintf(diagnostics, "ctx: credential write failed: %v\n", err)
				return 1
			}
			return 0
		}
	}
	fmt.Fprintln(diagnostics, "ctx: unsupported credential operation")
	return 2
}

func erase(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

var ErrExists = errors.New("credential already exists; pass --replace to overwrite")
