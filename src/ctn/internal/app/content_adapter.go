package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/webong/ext/ctn/internal/content"
	"github.com/webong/ext/pkg/ext-go/engine/host/adapter"
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

func (p *evaluatorPolicy) Isolation(backend content.Backend, selection string) (string, string, bool) {
	installed, err := p.store.List()
	if err != nil {
		return "unknown", err.Error(), false
	}
	for _, a := range installed {
		if a.Manifest.Name != backend.Name() {
			continue
		}
		command, err := a.Command(adapter.Invocation{Operation: "observe"})
		if err != nil {
			return "unknown", fmt.Sprintf("cannot observe: %v", err), false
		}
		var out strings.Builder
		command.Stdout = &out
		if err := command.Run(); err != nil {
			return "unknown", fmt.Sprintf("observe failed: %v", err), false
		}
		var observed struct {
			Contexts []struct {
				Selection  string            `json:"selection"`
				Attributes map[string]string `json:"attributes"`
			} `json:"contexts"`
		}
		if err := json.Unmarshal([]byte(out.String()), &observed); err != nil || len(observed.Contexts) == 0 {
			return "unknown", "no isolation reported", false
		}
		for _, ctx := range observed.Contexts {
			if selection != "" && ctx.Selection != selection {
				continue
			}
			isolation := ctx.Attributes["isolation"]
			if isolation == "" {
				isolation = "unreported"
			}
			if isolation != "sandboxed" {
				return isolation, fmt.Sprintf("has isolation %s, not sandboxed", isolation), false
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
				return "unknown", fmt.Sprintf("has no selection %q", selection), false
			}
		}
		return "sandboxed", "", true
	}
	return "unknown", "backend not found", false
}
