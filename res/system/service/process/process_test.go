package process

import (
	"os"
	"strings"
	"testing"
	"time"
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

func processTestSnapshot(at time.Time, scope, state string, ps ...ProcessInfo) ProcessSnapshot {
	result := ProcessSnapshot{HostID: "fixture-boot", Scope: scope, ObservedAt: at, Enumeration: CollectionStatus{State: state}, Processes: ps}
	stampProcessSnapshot(&result)
	return result
}
