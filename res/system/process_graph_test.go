package system

import (
	"context"
	"errors"
	"github.com/webong/ext/res/system/graph"
	"os"
	"testing"
	"time"

	"github.com/webong/ext/res/system/service/host"
	proc "github.com/webong/ext/res/system/service/process"
)

func processTestGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := New(graph.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
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
	child.Coverage["file"] = proc.CollectionStatus{State: "complete"}
	child.Resources = []proc.ProcessResource{{Kind: "file", Path: "/fixture/open"}}
	observe := func(s proc.ProcessSnapshot) {
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
	v, err := g.Store.GetVertex(ctx, Namespace, proc.InstanceID("fixture-boot", child))
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
	unknown := proc.ProcessInfo{PID: 20, Coverage: map[string]proc.CollectionStatus{"identity": proc.CollectionStatus{State: "permission-denied", Detail: os.ErrPermission.Error()}}}
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
	denied.Coverage["file"] = proc.CollectionStatus{State: "permission-denied", Detail: os.ErrPermission.Error()}
	observe(processTestSnapshot(at.Add(4*time.Second), "process", "complete", denied))
	if processTestCount(processTestStoreSnapshot(t, g), "process-resource") != 0 {
		t.Fatal("denied refresh retained old file evidence")
	}
	observe(processTestSnapshot(at.Add(5*time.Second), "process", "complete", child))
	// PID reuse is a new graph identity with no inherited resource nodes.
	replacement := processTestInfo(20, 10, "201")
	observe(processTestSnapshot(at.Add(6*time.Second), "process", "complete", replacement))
	if _, err := g.Store.GetVertex(ctx, Namespace, proc.InstanceID("fixture-boot", child)); !errors.Is(err, graph.ErrNotFound) {
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
	if err := g.ObserveHost(ctx, HostInventory{Shells: []host.ShellInfo{{Name: "fixture", Path: os.TempDir() + string(os.PathSeparator) + "fixture"}}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []proc.ProcessSnapshot{
		{}, processTestSnapshot(at, "bad", "complete"), processTestSnapshot(at, "process", "complete"), processTestSnapshot(at, "all", "bad"),
		processTestSnapshot(at, "all", "complete", processTestInfo(1, 0, "x"), processTestInfo(1, 0, "y")),
	} {
		if err := g.ObserveProcesses(ctx, s); err == nil {
			t.Fatalf("accepted invalid snapshot %+v", s)
		}
	}
	p := processTestInfo(30, 1, "x")
	p.Resources = []proc.ProcessResource{{Kind: "cookie"}}
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

func processTestInfo(pid, parent int, start string) proc.ProcessInfo {
	return proc.ProcessInfo{PID: pid, ParentPID: parent, StartID: start, Name: "fixture", Executable: "/fixture/program", Coverage: map[string]proc.CollectionStatus{"identity": {State: "complete"}}}
}

func processTestSnapshot(at time.Time, scope, state string, ps ...proc.ProcessInfo) proc.ProcessSnapshot {
	result := proc.ProcessSnapshot{HostID: "fixture-boot", Scope: scope, ObservedAt: at, Enumeration: proc.CollectionStatus{State: state, ObservedAt: &at}, Processes: ps}
	for i := range result.Processes {
		for category, status := range result.Processes[i].Coverage {
			status.ObservedAt = &at
			result.Processes[i].Coverage[category] = status
		}
	}
	return result
}
