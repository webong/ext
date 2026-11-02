package graph

import (
	"context"
	"testing"
	"time"

	"github.com/webong/ext/res/system/capture/surface"
	proc "github.com/webong/ext/res/system/service/process"
)

func TestSurfaceObservationRoundTripsThroughHostGraph(t *testing.T) {
	ctx := context.Background()
	projected, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer projected.Close()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	statusTime := now
	snapshot := surface.SurfaceSnapshot{
		Platform: "fixture", CoordinateSpace: "fixture-points", ObservedAt: now,
		Displays:      []surface.DisplayInfo{{ID: "display:opaque", Bounds: surface.SurfaceBounds{X: -10, Y: 0, Width: 1920, Height: 1080}, Scale: 2, Primary: true}},
		Windows:       []surface.WindowInfo{{ID: "window:opaque", PID: 42, ProcessStartID: "42:100", Bounds: surface.SurfaceBounds{X: 20, Y: 30, Width: 800, Height: 600}, OnScreen: true}},
		DisplayStatus: proc.CollectionStatus{State: "complete", ObservedAt: &statusTime},
		WindowStatus:  proc.CollectionStatus{State: "partial", Detail: "fixture limit", ObservedAt: &statusTime},
	}
	if err := projected.ObserveHost(ctx, HostInventory{Surfaces: &snapshot}); err != nil {
		t.Fatal(err)
	}
	stored, err := projected.ReadHost(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Surfaces == nil || len(stored.Surfaces.Displays) != 1 || len(stored.Surfaces.Windows) != 1 {
		t.Fatalf("surface observation did not round trip: %+v", stored.Surfaces)
	}
	if stored.Surfaces.Windows[0].ProcessStartID != "42:100" || stored.Surfaces.WindowStatus.State != "partial" {
		t.Fatalf("surface identity or status changed: %+v", stored.Surfaces)
	}
	if err := projected.ObserveHost(ctx, HostInventory{Surfaces: &surface.SurfaceSnapshot{
		Platform: "fixture", CoordinateSpace: "fixture-points", ObservedAt: now,
		Displays: []surface.DisplayInfo{}, Windows: []surface.WindowInfo{},
		DisplayStatus: proc.CollectionStatus{State: "complete", ObservedAt: &statusTime},
		WindowStatus:  proc.CollectionStatus{State: "complete", ObservedAt: &statusTime},
	}}); err != nil {
		t.Fatal(err)
	}
	stored, err = projected.ReadHost(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Surfaces == nil || len(stored.Surfaces.Displays) != 0 || len(stored.Surfaces.Windows) != 0 {
		t.Fatalf("empty observation did not remove stale surfaces: %+v", stored.Surfaces)
	}
}
