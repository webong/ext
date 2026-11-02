package system

import (
	"context"
	"testing"
	"time"

	"github.com/webong/ext/res/system/service/host"
	proc "github.com/webong/ext/res/system/service/process"
)

func TestObserveHostProjectsResourcesWithoutNetwork(t *testing.T) {
	g, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	now := time.Now().UTC()
	uptime := time.Hour
	boot := now.Add(-uptime)
	resources := &host.ResourceInfo{
		OS: "linux", Architecture: "amd64", ObservedAt: now, LogicalCPUs: 4,
		CPUCapacity: 2, CPUStatus: proc.CollectionStatus{State: "complete"},
		MemoryBytes: 8 << 30, MemoryCapacityBytes: 4 << 30, MemoryStatus: proc.CollectionStatus{State: "complete"},
		Container: host.ContainerInfo{Detected: true, State: host.ContainerDetected, Evidence: []string{"docker-marker"}},
		Uptime:    &uptime, BootTime: &boot, UptimeStatus: proc.CollectionStatus{State: "complete"},
	}
	if err = g.ObserveHost(context.Background(), HostInventory{Resources: resources}); err != nil {
		t.Fatal(err)
	}
	vertex, err := g.Store.GetVertex(context.Background(), Namespace, "host-resources/local")
	if err != nil {
		t.Fatal(err)
	}
	if vertex.Attributes["os"] != "linux" || vertex.Attributes["container"] == nil {
		t.Fatalf("attributes = %#v", vertex.Attributes)
	}
	for _, key := range []string{"interfaces", "network", "addresses"} {
		if _, exists := vertex.Attributes[key]; exists {
			t.Fatalf("resource projection duplicated network state under %q", key)
		}
	}
	if _, err = g.Store.GetEdge(context.Background(), Namespace, "machine-host-resources/local"); err != nil {
		t.Fatalf("resource edge missing: %v", err)
	}
	if err = g.ObserveHost(context.Background(), HostInventory{Resources: &host.ResourceInfo{OS: "linux"}}); err == nil {
		t.Fatal("incomplete resource observation accepted")
	}
}
