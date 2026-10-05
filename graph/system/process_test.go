package systemgraph

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/webong/ctx/graph"
)

func TestProcessOptionsAndCoverage(t *testing.T) {
	for _, o := range []ProcessOptions{{MaxProcesses: -1}, {MaxResources: -1}, {Timeout: -1}, {MaxProcesses: 65537}, {MaxResources: 65537}} {
		if _, err := processOptions(o); err == nil {
			t.Fatalf("accepted invalid options %+v", o)
		}
	}
	o, err := processOptions(ProcessOptions{})
	if err != nil || o.MaxProcesses != 4096 || o.MaxResources != 1024 || o.Timeout != 10*time.Second {
		t.Fatalf("defaults %+v: %v", o, err)
	}
	if got := processStatus(os.ErrPermission); got.State != "permission-denied" {
		t.Fatal(got)
	}
	if got := processStatus(os.ErrNotExist); got.State == "exited" {
		t.Fatal("missing resource does not prove process exit")
	}
	p := newProcessInfo(42)
	p.Coverage["file"] = completeProcessStatus()
	addProcessResource(&p, ProcessResource{Kind: "file", Path: "/a"}, 1)
	addProcessResource(&p, ProcessResource{Kind: "file", Path: "/b"}, 1)
	if len(p.Resources) != 1 || p.Coverage["file"].State != "partial" {
		t.Fatalf("limit not enforced: %+v", p)
	}
	p.Coverage["file"] = processStatus(os.ErrPermission)
	s := ProcessSnapshot{ObservedAt: time.Now(), Processes: []ProcessInfo{p}}
	stampProcessSnapshot(&s)
	if c := s.Processes[0].Coverage["file"]; c.State != "partial" || c.ObservedAt == nil {
		t.Fatalf("partial evidence lost: %+v", c)
	}
	addProcessResource(&p, ProcessResource{Kind: "mapping", Path: strings.Repeat("x", 4097)}, 10)
	if p.Coverage["mapping"].State != "partial" {
		t.Fatal("oversized evidence not marked partial")
	}
}

func processTestGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := New(graph.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}
func processTestSnapshot(at time.Time, scope, state string, ps ...ProcessInfo) ProcessSnapshot {
	result := ProcessSnapshot{HostID: "fixture-boot", Scope: scope, ObservedAt: at, Enumeration: CollectionStatus{State: state}, Processes: ps}
	stampProcessSnapshot(&result)
	return result
}
func processTestInfo(pid, parent int, start string) ProcessInfo {
	return ProcessInfo{PID: pid, ParentPID: parent, StartID: start, Name: "fixture", Executable: "/fixture/program", Coverage: map[string]CollectionStatus{"identity": completeProcessStatus()}}
}
func processTestStoreSnapshot(t *testing.T, g *Graph) graph.Snapshot {
	t.Helper()
	s, err := g.Store.Snapshot(context.Background(), Namespace)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func processTestCount(s graph.Snapshot, kind string) int {
	n := 0
	for _, v := range s.Vertices {
		if v.Kind == Namespace+"/"+kind {
			n++
		}
	}
	return n
}

func TestProcessProjectionLifecycle(t *testing.T) {
	ctx := context.Background()
	g := processTestGraph(t)
	at := time.Now().UTC()
	parent := processTestInfo(10, 1, "100")
	child := processTestInfo(20, 10, "200")
	child.Coverage["file"] = completeProcessStatus()
	child.Resources = []ProcessResource{{Kind: "file", Path: "/fixture/open"}}
	observe := func(s ProcessSnapshot) {
		t.Helper()
		if err := g.ObserveProcesses(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	observe(processTestSnapshot(at, "all", "complete", parent, child))
	s := processTestStoreSnapshot(t, g)
	if processTestCount(s, "process") != 2 || processTestCount(s, "process-resource") != 1 {
		t.Fatalf("unexpected records: %+v", s)
	}
	edges, err := g.Store.QueryEdges(ctx, graph.EdgeQuery{Namespace: Namespace, Type: Namespace + "/parent-of", Limit: 10})
	if err != nil || len(edges) != 1 {
		t.Fatalf("parent edges: %+v %v", edges, err)
	}
	// Identity-only scans preserve resource evidence and its original coverage time.
	observe(processTestSnapshot(at.Add(time.Second), "all", "complete", parent, processTestInfo(20, 10, "200")))
	s = processTestStoreSnapshot(t, g)
	if processTestCount(s, "process-resource") != 1 {
		t.Fatal("identity scan removed resources")
	}
	v, err := g.Store.GetVertex(ctx, Namespace, processInstanceID("fixture-boot", child))
	if err != nil {
		t.Fatal(err)
	}
	coverage := v.Attributes["coverage"].(map[string]any)["file"].(map[string]any)
	if coverage["observed_at"] != at.Format(time.RFC3339Nano) {
		t.Fatalf("old coverage falsely refreshed: %+v", coverage)
	}
	if _, err := g.ReadHost(ctx); err != nil {
		t.Fatalf("process marker broke host reading: %v", err)
	}
	// Limited scans and inaccessible identities cannot remove unseen instances.
	observe(processTestSnapshot(at.Add(2*time.Second), "all", "partial", parent))
	unknown := ProcessInfo{PID: 20, Coverage: map[string]CollectionStatus{"identity": processStatus(os.ErrPermission)}}
	observe(processTestSnapshot(at.Add(3*time.Second), "all", "complete", parent, unknown))
	if processTestCount(processTestStoreSnapshot(t, g), "process") != 2 {
		t.Fatal("denied identity removed known instance")
	}
	marker, err := g.Store.GetVertex(ctx, Namespace, "host-inventory/processes")
	if err != nil || marker.Attributes["unidentified_processes"] == nil {
		t.Fatalf("missing denied evidence: %v", err)
	}
	// Targeted permission failures clear previously observed resources for that kind.
	denied := processTestInfo(20, 10, "200")
	denied.Coverage["file"] = processStatus(os.ErrPermission)
	observe(processTestSnapshot(at.Add(4*time.Second), "process", "complete", denied))
	if processTestCount(processTestStoreSnapshot(t, g), "process-resource") != 0 {
		t.Fatal("denied refresh retained old file evidence")
	}
	observe(processTestSnapshot(at.Add(5*time.Second), "process", "complete", child))
	// PID reuse is a new graph identity with no inherited resource nodes.
	replacement := processTestInfo(20, 10, "201")
	observe(processTestSnapshot(at.Add(6*time.Second), "process", "complete", replacement))
	if _, err := g.Store.GetVertex(ctx, Namespace, processInstanceID("fixture-boot", child)); !errors.Is(err, graph.ErrNotFound) {
		t.Fatalf("old instance retained: %v", err)
	}
	if processTestCount(processTestStoreSnapshot(t, g), "process-resource") != 0 {
		t.Fatal("PID reuse inherited resources")
	}
	if err := g.ObserveProcesses(ctx, processTestSnapshot(at, "all", "complete")); err == nil {
		t.Fatal("stale observation accepted")
	}
	observe(processTestSnapshot(at.Add(7*time.Second), "all", "complete"))
	s = processTestStoreSnapshot(t, g)
	if processTestCount(s, "process") != 0 || processTestCount(s, "executable") != 0 {
		t.Fatal("complete scan left orphan process/executable")
	}
}

func TestProcessProjectionValidationAndIsolation(t *testing.T) {
	ctx := context.Background()
	g := processTestGraph(t)
	at := time.Now().UTC()
	if err := g.ObserveHost(ctx, HostInventory{Shells: []ShellInfo{{Name: "fixture", Path: os.TempDir() + string(os.PathSeparator) + "fixture"}}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []ProcessSnapshot{
		{}, processTestSnapshot(at, "bad", "complete"), processTestSnapshot(at, "process", "complete"), processTestSnapshot(at, "all", "bad"),
		processTestSnapshot(at, "all", "complete", processTestInfo(1, 0, "x"), processTestInfo(1, 0, "y")),
	} {
		if err := g.ObserveProcesses(ctx, s); err == nil {
			t.Fatalf("accepted invalid snapshot %+v", s)
		}
	}
	p := processTestInfo(30, 1, "x")
	p.Resources = []ProcessResource{{Kind: "cookie"}}
	if err := g.ObserveProcesses(ctx, processTestSnapshot(at, "all", "complete", p)); err == nil {
		t.Fatal("accepted product-specific resource")
	}
	if err := g.ObserveProcesses(ctx, processTestSnapshot(at, "all", "complete")); err != nil {
		t.Fatal(err)
	}
	if processTestCount(processTestStoreSnapshot(t, g), "shell") != 1 {
		t.Fatal("process reconciliation deleted shell inventory")
	}
}
