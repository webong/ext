package ledger_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	ledger "github.com/webong/ext/pkg/ext-go/engine/ledger"
	"github.com/webong/ext/pkg/ext-go/engine/ledger/ledgertest"
	process "github.com/webong/ext/res/system/service/process"
)

func open(t *testing.T) *ledger.Graph {
	t.Helper()
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	n, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Bind(n)
	if err != nil {
		t.Fatal(err)
	}
	s, err := l.OpenStore(context.Background(), "memory", "", ledger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := ledger.NewGraph(context.Background(), s)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	return g
}
func TestNativeRecorderAndInventory(t *testing.T) {
	g := open(t)
	ctx := context.Background()
	if err := g.ObserveShell(ctx, t.TempDir(), "fixture", map[string]string{"browser": "work"}); err != nil {
		t.Fatal(err)
	}
	if err := g.ObserveInventory(ctx, []ledger.AdapterObservation{{Name: "fixture", Runtime: "browser", Trusted: true, Listed: true, ListSupported: true, DiscoveryStatus: "ok", Capabilities: []string{"open"}, Supports: []string{"web"}, Contexts: []ledger.ContextObservation{{Selection: "work"}}, Resources: []ledger.ResourceObservation{{ID: "one", Kind: "page", Context: "work"}}}}, nil); err != nil {
		t.Fatal(err)
	}
	inv, err := g.ResolveInventory(ctx, "browser")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Contexts) != 1 || inv.Contexts[0].Selection != "work" || inv.Contexts[0].Adapter != "fixture" {
		t.Fatalf("inventory: %+v", inv)
	}
	snap, err := g.Store.Snapshot(ctx, ledger.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	var shell, resource bool
	for _, v := range snap.Vertices {
		shell = shell || v.Kind == "ext.system/shell-session"
		resource = resource || v.Kind == "ext.system/resource"
	}
	if !shell || !resource {
		t.Fatalf("missing C recorder facts: %+v", snap)
	}
	if err := g.ObserveInventory(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	inv, err = g.ResolveInventory(ctx, "browser")
	if err != nil || len(inv.Contexts) != 0 {
		t.Fatalf("stale inventory: %+v %v", inv, err)
	}
}
func TestNativeProcessAndCancellation(t *testing.T) {
	g := open(t)
	if code := ledger.Code(g.Store.Register(context.Background(), ledger.Schema{Namespace: "bad namespace", Version: "1"})); code != "invalid" {
		t.Fatalf("native error code: %s", code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.ObserveShell(ctx, t.TempDir(), "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	snap, err := g.Store.Snapshot(context.Background(), ledger.Namespace)
	if err != nil || len(snap.Vertices) != 0 {
		t.Fatalf("canceled call wrote graph: %v %v", snap, err)
	}
	observed, err := g.InspectProcess(context.Background(), os.Getpid(), process.ProcessOptions{MaxResources: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Processes) != 1 || observed.Processes[0].PID != os.Getpid() {
		t.Fatalf("wrong process: %+v", observed)
	}
}

func TestNativeClose(t *testing.T) {
	g := open(t)
	if code := ledger.Code(g.Store.Close()); code != "busy" {
		t.Fatalf("store with live recorder: %s", code)
	}
	for i := 0; i < 2; i++ {
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Store.Snapshot(context.Background(), ledger.Namespace); !errors.Is(err, ledger.ErrClosed) {
		t.Fatalf("closed store: %v", err)
	}
}

func TestNativeStoreConformance(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	n, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Bind(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			b := ledgertest.Backend{Durable: backend == "file", Open: func(t testing.TB, dir string, o ledger.Options) ledger.Store {
				s, err := l.OpenStore(context.Background(), backend, dir, o)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}}
			if backend == "file" {
				ledgertest.Run(t, b)
			}
			ledgertest.RunBehaviour(t, b)
		})
	}
}
