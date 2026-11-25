package nativeplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// GraphOptions is application policy. Review approves selected package bytes;
// Authorize decides each reverse call. The caller coordinates source review
// with writers. C owns planning, private copies and all process lifetimes.
type GraphOptions struct {
	RootID   string
	Contract plugin.ContractRef
	OS, Arch string
	MaxHosts int
	Layout   storelayout.Layout
	// LedgerPath enables durable observations in a caller-owned ledger.
	LedgerPath string
	Review     func(packagekit.Manifest) (string, error)
	Authorize  func(context.Context, plugin.Identity, plugin.Identity, plugin.Request) error
}

// Graph exposes the C graph's root call interface. Closing it closes the whole
// graph; no provider handle or snapshot ownership escapes into the application.
type Graph struct {
	Root        *library.Object
	once        sync.Once
	stop        func() bool
	mu          sync.Mutex
	closed      bool
	closeErr    error
	publication *publication
}

func (g *Graph) closeObject() {
	g.once.Do(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.closed = true
		g.closeErr = g.Root.Close()
		if g.publication != nil {
			// Never clear observations until native shutdown has succeeded.
			if g.closeErr == nil {
				g.closeErr = g.publication.finish()
			}
			g.closeErr = errors.Join(g.closeErr, g.publication.store.Close())
		}
	})
}
func (g *Graph) Close() error {
	if g.stop != nil {
		g.stop()
	}
	g.closeObject()
	return g.closeErr
}

func OpenGraph(ctx context.Context, lib *library.Described, store *library.Object, options GraphOptions) (_ *Graph, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Review == nil {
		return nil, fmt.Errorf("provider graph requires package review")
	}
	limit := options.MaxHosts
	if limit == 0 {
		limit = 128
	}
	if limit < 1 || limit > 1024 {
		return nil, fmt.Errorf("provider graph host limit must be 1..1024")
	}
	var rows []struct {
		Manifest  packagekit.Manifest `json:"manifest"`
		Directory string              `json:"directory"`
	}
	if err := store.Call("list", nil, &rows); err != nil {
		return nil, err
	}
	byID := map[string]packagekit.Manifest{}
	for _, row := range rows {
		byID[row.Manifest.Name()] = row.Manifest
	}
	selected, ok := byID[options.RootID]
	if !ok {
		return nil, fmt.Errorf("provider %s not found", options.RootID)
	}
	config := map[string]any{
		"packages": rows, "os": options.OS, "arch": options.Arch, "max_hosts": limit, "temp_root": os.TempDir(),
		"root": map[string]any{"identity": selected.Descriptor.Identity, "contract": options.Contract},
	}
	if options.Layout != (storelayout.Layout{}) {
		config["layout"] = options.Layout
	}
	owned, err := lib.Open("execution_graph", config, library.Handlers{"reverse_authorize": func(data json.RawMessage) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if options.Authorize == nil {
			return nil, plugin.ErrDenied
		}
		var event struct {
			Consumer, Provider plugin.Identity
			Request            plugin.Request
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return nil, options.Authorize(ctx, event.Consumer, event.Provider, event.Request)
	}})
	if err != nil {
		return nil, err
	}
	g := &Graph{Root: owned}
	g.stop = context.AfterFunc(ctx, g.closeObject)
	defer func() {
		if resultErr != nil {
			g.Close()
		}
	}()
	var plan struct {
		Nodes []struct{ Identity plugin.Identity }
	}
	if err := owned.Call("plan", nil, &plan); err != nil {
		return nil, err
	}
	reviews := []map[string]any{}
	seen := map[plugin.Identity]bool{}
	for _, node := range plan.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[node.Identity] {
			continue
		}
		manifest, ok := byID[node.Identity.ID]
		if !ok || manifest.Descriptor.Identity != node.Identity {
			return nil, fmt.Errorf("native plan package identity changed")
		}
		digest, err := options.Review(manifest)
		if err != nil {
			return nil, err
		}
		if digest == "" {
			return nil, fmt.Errorf("review of %s returned no digest", node.Identity.ID)
		}
		reviews = append(reviews, map[string]any{"identity": node.Identity, "digest": digest})
		seen[node.Identity] = true
	}
	if err := owned.Call("start", map[string]any{"reviews": reviews, "timeout_ms": 5000}, nil); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.LedgerPath != "" {
		g.mu.Lock()
		if g.closed {
			err = context.Canceled
		} else {
			g.publication, err = publishGraph(ctx, owned, options.LedgerPath)
		}
		g.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return g, nil
}
