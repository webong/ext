//go:build darwin

package system

import (
	"context"
	"testing"
)

// TestNativeResourceObservation exercises the real sysctl and boot-time reads.
// It asserts only stable invariants, not this machine's exact capacity.
func TestNativeResourceObservation(t *testing.T) {
	resources, err := DiscoverResources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resources.OS != "darwin" || resources.Architecture == "" || resources.LogicalCPUs < 1 {
		t.Fatalf("identity = %#v", resources)
	}
	if resources.CPUCapacity != float64(resources.LogicalCPUs) || resources.CPUStatus.State != "complete" {
		t.Fatalf("cpu = %v %#v", resources.CPUCapacity, resources.CPUStatus)
	}
	if resources.MemoryBytes == 0 || resources.MemoryCapacityBytes == 0 || resources.MemoryStatus.State != "complete" {
		t.Fatalf("memory = %#v", resources.MemoryStatus)
	}
	if resources.Uptime == nil || *resources.Uptime <= 0 || resources.BootTime == nil || resources.UptimeStatus.State != "complete" {
		t.Fatalf("uptime = %#v", resources.UptimeStatus)
	}
	if resources.Container.State != ContainerUnsupported {
		t.Fatalf("container = %#v", resources.Container)
	}
}
