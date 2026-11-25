package ledger

import (
	"context"
	"errors"
	"sync"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	wire "github.com/webong/ext/pkg/ext-go/core/ledger/observation"
)

type Graph struct {
	mu             sync.Mutex
	recorderClosed bool
	Store          *NativeStore
	recorder       *library.Object
}

// NewGraph opens a C recorder on a C store. The graph owns the store on success.
func NewGraph(ctx context.Context, s *NativeStore) (*Graph, error) {
	if s == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	r, err := s.library.api.Open("recorder", map[string]any{"store": s.object.Handle(), "machine": true}, nil)
	if err != nil {
		return nil, err
	}
	return &Graph{Store: s, recorder: r}, nil
}
func (g *Graph) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.recorderClosed {
		if err := g.recorder.Close(); err != nil {
			return err
		}
		g.recorderClosed = true
	}
	return g.Store.Close()
}
func (g *Graph) Record(ctx context.Context, op string, input, out any) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.recorderClosed {
		return ErrClosed
	}
	return call(ctx, g.recorder, "record", map[string]any{"operation": op, "input": input}, out)
}
func (g *Graph) Collect(ctx context.Context, what string, options map[string]any, out any) error {
	return g.Store.library.Collect(ctx, what, options, out)
}
func (g *Graph) ObserveShell(ctx context.Context, cwd, profile string, selections map[string]string) error {
	return g.Record(ctx, "observe_shell", map[string]any{"cwd": cwd, "profile": profile, "selections": selections}, nil)
}
func (g *Graph) ObserveWeb(ctx context.Context, provider, profile, scheme, host string) error {
	return g.Record(ctx, "observe_web", map[string]any{"provider": provider, "profile": profile, "scheme": scheme, "host": host}, nil)
}
func (g *Graph) ObserveHost(ctx context.Context, in HostInventory) error {
	return g.Record(ctx, "observe_host", in, nil)
}
func (g *Graph) ObserveProcesses(ctx context.Context, in wire.ProcessSnapshot) error {
	return g.Record(ctx, "observe_processes", in, nil)
}
func (g *Graph) ReadHost(ctx context.Context) (out HostSnapshot, err error) {
	err = g.Record(ctx, "read_host", map[string]any{}, &out)
	return
}
func (g *Graph) ResolveInventory(ctx context.Context, runtime string) (out Inventory, err error) {
	err = g.Record(ctx, "resolve_inventory", map[string]any{"runtime": runtime}, &out)
	return
}
func (g *Graph) ScanHost(ctx context.Context) (out HostInventory, err error) {
	err = g.Collect(ctx, "host", nil, &out)
	if err == nil {
		err = g.ObserveHost(ctx, out)
	}
	return
}
func (g *Graph) ScanProcesses(ctx context.Context, opts wire.ProcessOptions) (out wire.ProcessSnapshot, err error) {
	return g.processes(ctx, 0, opts)
}
func (g *Graph) InspectProcess(ctx context.Context, pid int, opts wire.ProcessOptions) (out wire.ProcessSnapshot, err error) {
	return g.processes(ctx, pid, opts)
}
func (g *Graph) processes(ctx context.Context, pid int, opts wire.ProcessOptions) (out wire.ProcessSnapshot, err error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	in := map[string]any{}
	what := "processes"
	if pid != 0 {
		what = "process"
		in["pid"] = pid
		if opts.MaxResources != 0 {
			in["resource_limit"] = opts.MaxResources
		}
	} else if opts.MaxProcesses != 0 {
		in["limit"] = opts.MaxProcesses
	}
	err = g.Collect(ctx, what, in, &out)
	if err == nil {
		err = g.ObserveProcesses(ctx, out)
	}
	return
}
func (g *Graph) ObserveInventory(ctx context.Context, adapters []AdapterObservation, aliases []AliasObservation, installers ...InstallerObservation) error {
	as := make([]any, 0, len(adapters))
	for _, a := range adapters {
		rs := make([]any, 0, len(a.Resources))
		for _, r := range a.Resources {
			rs = append(rs, map[string]any{"id": r.ID, "kind": r.Kind, "context": r.Context, "attributes": r.Attributes})
		}
		es := make([]any, 0, len(a.Relations))
		for _, e := range a.Relations {
			es = append(es, map[string]any{"from": e.From, "to": e.To, "kind": e.Kind})
		}
		as = append(as, map[string]any{"name": a.Name, "runtime": a.Runtime, "selector": a.Selector, "discovery_status": a.DiscoveryStatus, "surfaces": a.Surfaces, "capabilities": a.Capabilities, "supports": a.Supports, "engine": a.Engine, "role": a.Role, "browser_share": a.BrowserShare, "trusted": a.Trusted, "list_supported": a.ListSupported, "observe_supported": a.ObserveSupported, "listed": a.Listed, "contexts": a.Contexts, "programs": a.Programs, "resources": rs, "relations": es})
	}
	als := make([]any, 0, len(aliases))
	for _, a := range aliases {
		als = append(als, map[string]any{"space": a.Space, "name": a.Name, "adapter": a.Adapter, "selection": a.Selection, "metadata": a.Metadata})
	}
	ins := make([]any, 0, len(installers))
	for _, i := range installers {
		ins = append(ins, map[string]any{"name": i.Name, "source": i.Source})
	}
	return g.Record(ctx, "observe_inventory", map[string]any{"adapters": as, "aliases": als, "installers": ins}, nil)
}

// ObserveRequirements refreshes requested observations through C; consumers select and validate resources.
func (g *Graph) ObserveRequirements(ctx context.Context, r OperationRequirements) (HostSnapshot, error) {
	if r.Operation == "" || (r.Shell == nil && r.Filesystem == nil && r.Webview == nil) {
		return HostSnapshot{}, errors.New("host requirements need an operation and at least one requirement")
	}
	// Refresh the requested categories. Selection is observation, not a lease;
	// preparation performs its own final filesystem and backend checks.
	var in HostInventory
	if r.Shell != nil {
		options := map[string]any{}
		if r.Shell.SearchPath != nil {
			options["search_path"] = *r.Shell.SearchPath
		}
		if err := g.Collect(ctx, "shells", options, &in.Shells); err != nil {
			return HostSnapshot{}, err
		}
	}
	if r.Filesystem != nil {
		if err := g.Collect(ctx, "filesystems", nil, &in.Filesystems); err != nil {
			return HostSnapshot{}, err
		}
	}
	if r.Webview != nil {
		if err := g.Collect(ctx, "webviews", nil, &in.Webviews); err != nil {
			return HostSnapshot{}, err
		}
	}
	if err := g.ObserveHost(ctx, in); err != nil {
		return HostSnapshot{}, err
	}
	out, err := g.ReadHost(ctx)
	return out, err
}
