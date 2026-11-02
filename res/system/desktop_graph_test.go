package system

import (
	"context"
	"testing"
	"time"

	"github.com/webong/ext/res/system/capture/surface"
)

func TestDesktopObservationRoundTripsThroughHostGraph(t *testing.T) {
	ctx := context.Background()
	graph, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer graph.Close()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	desktop := surface.DesktopInfo{Platform: "fixture", Interactive: true, Capabilities: []string{surface.DesktopNative},
		Recording: surface.RecordingInfo{Status: "unavailable", Permission: "not-required", CheckedAt: now}, ObservedAt: now}
	if err := graph.ObserveHost(ctx, HostInventory{Desktop: &desktop}); err != nil {
		t.Fatal(err)
	}
	stored, err := graph.ReadHost(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Desktop == nil || stored.Desktop.Platform != "fixture" || !stored.Desktop.Interactive {
		t.Fatalf("desktop observation did not round trip: %+v", stored.Desktop)
	}
}
