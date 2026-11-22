package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/pkg/ext-go/engine/host/adapter"
	"github.com/webong/ext/src/ctn/internal/content"
)

// evaluatorBackend wraps an evaluator adapter as a content.Backend.
type evaluatorBackend struct {
	adapter *adapter.Adapter
	prefix  string
}

func (b *evaluatorBackend) Name() string {
	return b.adapter.Manifest.Name
}

func (b *evaluatorBackend) Run(ctx context.Context, arguments []string) (int, []byte, error) {
	// Prepend the prefix (for example "@" for evm) to the file argument,
	// which is always the first argument.
	if len(arguments) > 0 && b.prefix != "" {
		arguments[0] = b.prefix + arguments[0]
	}
	command, err := b.adapter.CommandContext(ctx, adapter.Invocation{
		Operation: "run",
		Arguments: arguments,
	})
	if err != nil {
		return 0, nil, err
	}
	var out, stderr strings.Builder
	command.Stdout = &out
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			return exit.ExitCode(), []byte(out.String() + stderr.String()), err
		default:
			return 0, []byte(out.String() + stderr.String()), err
		}
	}
	return 0, []byte(out.String()), nil
}

// evaluatorPolicy wraps trust and isolation checks from the evaluator adapter store.
type evaluatorPolicy struct {
	store *adapter.Store
}

func (p *evaluatorPolicy) IsTrusted(backend content.Backend) bool {
	installed, err := p.store.List()
	if err != nil {
		return false
	}
	for _, a := range installed {
		if a.Manifest.Name == backend.Name() {
			ok, _ := p.store.IsTrusted(a)
			return ok
		}
	}
	return false
}

// sandboxedBoundaries are the boundaries at which an evaluator's "sandboxed" claim
// can hold (docs/adr-sandboxing.md): the program has no direct syscall access to
// the host, or reaches it only through OS confinement.
var sandboxedBoundaries = []string{cgoengine.BoundaryConfined, cgoengine.BoundaryRuntime, cgoengine.BoundaryContainer, cgoengine.BoundaryKernel}

// checkIsolation holds one context's reported isolation and boundary to the rule:
// a boundary must be reported, be a known level, and for "sandboxed" be one of
// sandboxedBoundaries. Only "sandboxed" permits execution.
func checkIsolation(isolation, boundary string) (string, bool) {
	if isolation == "" {
		isolation = "unreported"
	}
	if boundary == "" {
		return "reports no boundary", false
	}
	if !cgoengine.ValidBoundary(boundary) {
		return fmt.Sprintf("reports an unknown boundary %q", boundary), false
	}
	if isolation != "sandboxed" {
		return fmt.Sprintf("has isolation %s, not sandboxed", isolation), false
	}
	if !slices.Contains(sandboxedBoundaries, boundary) {
		return fmt.Sprintf("claims sandboxed at boundary %s, which cannot be", boundary), false
	}
	return "", true
}

func (p *evaluatorPolicy) Isolation(backend content.Backend, selection string) (string, string, string, bool) {
	installed, err := p.store.List()
	if err != nil {
		return "unknown", "", err.Error(), false
	}
	for _, a := range installed {
		if a.Manifest.Name != backend.Name() {
			continue
		}
		command, err := a.Command(adapter.Invocation{Operation: "observe"})
		if err != nil {
			return "unknown", "", fmt.Sprintf("cannot observe: %v", err), false
		}
		var out strings.Builder
		command.Stdout = &out
		if err := command.Run(); err != nil {
			return "unknown", "", fmt.Sprintf("observe failed: %v", err), false
		}
		var observed struct {
			Contexts []struct {
				Selection  string            `json:"selection"`
				Attributes map[string]string `json:"attributes"`
			} `json:"contexts"`
		}
		if err := json.Unmarshal([]byte(out.String()), &observed); err != nil || len(observed.Contexts) == 0 {
			return "unknown", "", "no isolation reported", false
		}
		boundary := ""
		for _, ctx := range observed.Contexts {
			if selection != "" && ctx.Selection != selection {
				continue
			}
			isolation, b := ctx.Attributes["isolation"], ctx.Attributes["boundary"]
			if reason, ok := checkIsolation(isolation, b); !ok {
				if isolation == "" {
					isolation = "unreported"
				}
				return isolation, b, reason, false
			}
			if boundary == "" {
				boundary = b
			} else if boundary != b {
				boundary = "mixed"
			}
		}
		if selection != "" {
			found := false
			for _, ctx := range observed.Contexts {
				if ctx.Selection == selection {
					found = true
					break
				}
			}
			if !found {
				return "unknown", "", fmt.Sprintf("has no selection %q", selection), false
			}
		}
		return "sandboxed", boundary, "", true
	}
	return "unknown", "", "backend not found", false
}
