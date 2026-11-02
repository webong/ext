//go:build darwin || linux || windows

package system

import (
	"context"
	"os"
	"testing"
	"time"

	proc "github.com/webong/ext/res/system/service/process"
)

// The collectors are tested against real child processes in service/process. This
// opt-in test checks the other half on a live machine: what the collector reports for
// this very process is recorded in the graph, and observing a fuller inspection of it
// keeps the same record.
func TestLiveProcessProjection(t *testing.T) {
	if os.Getenv("EXT_GRAPH_PROCESS_NATIVE_TESTS") != "1" {
		t.Skip("set EXT_GRAPH_PROCESS_NATIVE_TESTS=1 to run isolated native process inspection")
	}
	ctx := context.Background()
	options := proc.ProcessOptions{Timeout: 20 * time.Second, MaxProcesses: 65536, MaxResources: 4096}
	g := processTestGraph(t)
	all, err := g.ScanProcesses(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	var self *proc.ProcessInfo
	for i := range all.Processes {
		if all.Processes[i].PID == os.Getpid() {
			self = &all.Processes[i]
		}
	}
	if self == nil {
		t.Fatal("this process is absent from the inventory")
	}
	id := proc.InstanceID(all.HostID, *self)
	if _, err = g.Store.GetVertex(ctx, Namespace, id); err != nil {
		t.Fatalf("scanned process not recorded: %v", err)
	}
	inspection, err := proc.InspectProcess(ctx, os.Getpid(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.ObserveProcesses(ctx, inspection); err != nil {
		t.Fatal(err)
	}
	if _, err = g.Store.GetVertex(ctx, Namespace, id); err != nil {
		t.Fatalf("inspected process lost its record: %v", err)
	}
}
