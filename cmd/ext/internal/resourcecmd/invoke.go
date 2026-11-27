package resourcecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/cmd/ext/internal/packagelock"
	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// invoke performs explicit discovery or a resource-bound call. Package approval
// permits launch; the provider must enforce its resource policy. Inspection and
// invocation share a graph and writer lock. Reverse calls require explicit grants.
func (m Manager) invoke(ctx context.Context, op, id string, request json.RawMessage, grants []DependencyGrant) (result json.RawMessage, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(request) > 1<<20 {
		return nil, fmt.Errorf("resource request exceeds 1 MiB")
	}
	lib, err := nativeplugin.Open()
	if err != nil {
		return nil, err
	}
	var invocation *callRequest
	if op == "call" {
		if err := lib.Library().Call("resource.call.request.validate", request, nil); err != nil {
			return nil, err
		}
		invocation = new(callRequest)
		if err := json.Unmarshal(request, invocation); err != nil {
			return nil, err
		}
		request, err = json.Marshal(map[string]any{"resource": invocation.Resource})
		if err != nil {
			return nil, err
		}
		op = "inspect"
	}
	if err := lib.Library().Call("resource.request.validate", map[string]any{"operation": op, "request": request}, nil); err != nil {
		return nil, err
	}
	// Hold the CLI transaction boundary through the bounded invocation so another
	// participating writer cannot revoke/replace a checked package mid-launch.
	release, err := packagelock.Acquire(m.Home)
	if err != nil {
		return nil, err
	}
	defer release()
	root := filepath.Join(m.Home, "packages")
	s, err := lib.Open("store", map[string]any{"root": root, "layout": storelayout.Ext()}, nil)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var manifest packagekit.Manifest
	if err := s.Call("load", map[string]any{"id": id}, &manifest); err != nil {
		return nil, err
	}
	ref := plugin.ContractRef{Name: "ext.resource", Version: "v1"}
	if err := lib.Library().Call("descriptor.select", map[string]any{"candidates": []plugin.Descriptor{manifest.Descriptor}, "requirement": packagekit.Dependency{Contract: ref, Operation: op}}, nil); err != nil {
		return nil, err
	}
	if op == "inspect" {
		var input struct {
			Resource struct{ Provider plugin.Identity }
		}
		if err := json.Unmarshal(request, &input); err != nil {
			return nil, err
		}
		if input.Resource.Provider != manifest.Descriptor.Identity {
			return nil, fmt.Errorf("resource provider identity does not match selected package")
		}
	}
	local, err := store.NewWithLayout(root, store.Limits{}, storelayout.Ext())
	if err != nil {
		return nil, err
	}
	ledgerPath := nativeplugin.LedgerPath(m.Home)
	var peers []nativeplugin.Peer
	if len(grants) > 0 {
		live, err := nativeplugin.LivePeers(ctx, ledgerPath)
		if err != nil {
			return nil, err
		}
		for _, peer := range live {
			descriptor := peer.Descriptor
			for _, grant := range grants {
				if grant.Provider == descriptor.Identity {
					if _, err := descriptor.Lookup(grant.Contract, grant.Operation); err == nil {
						peers = append(peers, peer)
						break
					}
				}
			}
		}
	}
	if err := (pkgcmd.Manager{Store: local}).CheckInvocationWithPeers(id, peers); err != nil {
		return nil, err
	}
	graph, err := nativeplugin.OpenGraph(ctx, lib, s, nativeplugin.GraphOptions{
		RootID: id, Contract: ref, OS: runtime.GOOS, Arch: runtime.GOARCH, Layout: local.Layout(),
		LedgerPath: ledgerPath, Peers: peers,
		Review: func(pm packagekit.Manifest) (string, error) {
			return nativeplugin.ReviewInstalled(lib, root, local.Layout(), pm.Name())
		},
		Authorize: func(_ context.Context, _ plugin.Identity, provider plugin.Identity, request plugin.Request) error {
			// Only reverse calls scoped to an active forward call may spend a grant.
			if request.Parent == "" {
				return plugin.ErrDenied
			}
			for _, grant := range grants {
				if grant.Provider == provider && grant.Contract == request.Contract && grant.Operation == request.Operation {
					return nil
				}
			}
			return plugin.ErrDenied
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, graph.Close())
		if err := ctx.Err(); err != nil {
			result, resultErr = nil, err
		}
	}()
	host := graph.Root
	var envelope plugin.Response
	if err := host.Call("call", map[string]any{"call": map[string]any{"contract": ref, "operation": op, "payload": request}, "timeout_ms": 5000}, &envelope); err != nil {
		return nil, err
	}
	if envelope.Error != nil {
		if err := lib.Library().Call("resource.error.validate", map[string]any{"operation": op, "request": request, "error": envelope.Error}, nil); err != nil {
			return nil, fmt.Errorf("invalid resource error: %w", err)
		}
		return nil, envelope.Error
	}
	response := envelope.Payload
	if err := lib.Library().Call("resource.response.validate", map[string]any{"operation": op, "provider": manifest.Descriptor.Identity, "request": request, "response": response}, nil); err != nil {
		return nil, err
	}
	if invocation != nil {
		return invokeResource(lib, host, manifest.Descriptor, response, *invocation)
	}
	return response, nil
}
