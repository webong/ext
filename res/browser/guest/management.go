package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/webong/ctx/res/browser/contract"
)

// ManagementBackend owns browser-specific extension, userscript, and
// bookmarklet work. Implementations may delegate page/session work to a
// separate browser runtime while keeping installation routes native.
type ManagementBackend interface {
	ManageBrowser(context.Context, string, contract.Request) (contract.Response, error)
}

func RunManagement(ctx context.Context, profile string, input io.Reader, stdout, stderr io.Writer, backend ManagementBackend) int {
	if backend == nil {
		fmt.Fprintln(stderr, "browser management backend is required")
		return 2
	}
	decoder := json.NewDecoder(io.LimitReader(input, 8<<20+1))
	var request contract.Request
	if err := decoder.Decode(&request); err != nil {
		fmt.Fprintf(stderr, "invalid browser management request: %v\n", err)
		return 2
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fmt.Fprintln(stderr, "browser management request contains trailing data")
		return 2
	}
	if err := contract.ValidateRequest(request); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if ctx == nil {
		ctx = context.Background()
	}
	response, err := backend.ManageBrowser(ctx, profile, request)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := contract.ValidateResponse(response, request); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
