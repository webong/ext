// Package resourcecmd exposes offline providers, explicit discovery and resource-bound calls.
package resourcecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

type Manager struct{ Home string }

// DependencyGrant authorizes one provider operation for this invocation only.
// The provider's complete identity is pinned; package trust is checked separately.
type DependencyGrant struct {
	Provider  plugin.Identity    `json:"provider"`
	Contract  plugin.ContractRef `json:"contract"`
	Operation string             `json:"operation"`
}

type Provider struct {
	Descriptor  plugin.Descriptor `json:"descriptor"`
	Observation string            `json:"observation"`
}

// Run distinguishes offline declarations from explicitly requested live operations.
// Listing a declaration grants no authority to launch or access the provider.
func (m Manager) Run(args []string, out, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return m.RunContext(ctx, args, out, stderr)
}

// RunContext allows a caller to cancel live discovery or a resource operation.
// Cancellation closes the native host and joins its work before releasing the
// package writer lock; the session is not reused.
func (m Manager) RunContext(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) >= 3 && (args[0] == "list" || args[0] == "inspect" || args[0] == "call") {
		grants, err := parseGrants(args[3:])
		if err != nil {
			fmt.Fprintln(stderr, "ext: resource:", err)
			return 2
		}
		result, err := m.invoke(ctx, args[0], args[1], json.RawMessage(args[2]), grants)
		if err == nil {
			err = json.NewEncoder(out).Encode(result)
		}
		if err != nil {
			fmt.Fprintln(stderr, "ext: resource:", err)
			return 1
		}
		return 0
	}
	if len(args) != 1 || args[0] != "providers" {
		fmt.Fprintln(stderr, "usage: ext resource providers | list|inspect|call <provider-id> <request-json> [--allow-dependency <grant-json>]...")
		return 2
	}
	providers, err := m.providers()
	if err == nil {
		err = json.NewEncoder(out).Encode(providers)
	}
	if err != nil {
		fmt.Fprintln(stderr, "ext: resource providers:", err)
		return 1
	}
	return 0
}

func parseGrants(args []string) ([]DependencyGrant, error) {
	if len(args)%2 != 0 || len(args) > 128 {
		return nil, fmt.Errorf("expected at most 64 --allow-dependency <grant-json> pairs")
	}
	var grants []DependencyGrant
	seen := map[DependencyGrant]bool{}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "--allow-dependency" || len(args[i+1]) > 4096 {
			return nil, fmt.Errorf("invalid dependency grant option")
		}
		var grant DependencyGrant
		if err := plugin.Decode([]byte(args[i+1]), &grant); err != nil {
			return nil, fmt.Errorf("dependency grant: %w", err)
		}
		d := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: grant.Provider, Contracts: []plugin.Contract{{ContractRef: grant.Contract, Operations: []plugin.Operation{{Name: grant.Operation}}}}}
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("dependency grant: %w", err)
		}
		if seen[grant] {
			return nil, fmt.Errorf("duplicate dependency grant")
		}
		seen[grant] = true
		grants = append(grants, grant)
	}
	return grants, nil
}

func (m Manager) providers() ([]Provider, error) {
	lib, err := nativeplugin.Open()
	if err != nil {
		return nil, err
	}
	s, err := lib.Open("store", map[string]any{"root": filepath.Join(m.Home, "packages")}, nil)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var rows []struct {
		Manifest packagekit.Manifest `json:"manifest"`
	}
	if err := s.Call("list", nil, &rows); err != nil {
		return nil, err
	}
	providers := []Provider{}
	for _, row := range rows {
		descriptor := row.Manifest.Descriptor
		matches := true
		for _, op := range []string{"list", "inspect"} {
			err := lib.Library().Call("descriptor.select", map[string]any{
				"candidates":  []plugin.Descriptor{descriptor},
				"requirement": packagekit.Dependency{Contract: plugin.ContractRef{Name: "ext.resource", Version: "v1"}, Operation: op},
			}, nil)
			if err != nil {
				var status *library.CodeError
				if errors.As(err, &status) && status.Name == "not_found" {
					matches = false
					break
				}
				return nil, err
			}
		}
		if matches {
			providers = append(providers, Provider{Descriptor: descriptor, Observation: "manifest"})
		}
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Descriptor.Identity.ID < providers[j].Descriptor.Identity.ID })
	return providers, nil
}
