package system

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSurfaceOptionsAreBounded(t *testing.T) {
	if _, err := DiscoverSurfaces(context.Background(), SurfaceOptions{MaxWindows: -1}); err == nil {
		t.Fatal("negative window limit was accepted")
	}
	if _, err := DiscoverSurfaces(context.Background(), SurfaceOptions{MaxDisplays: 65537}); err == nil {
		t.Fatal("excessive display limit was accepted")
	}
}

func TestSurfaceObservationContainsNoTitlesOrNativeHandles(t *testing.T) {
	snapshot, err := DiscoverSurfaces(context.Background(), SurfaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, forbidden := range []string{"window_title", "application_name", "native_handle", "window_id", "display_id"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("surface observation exposed %q: %s", forbidden, encoded)
		}
	}
	if snapshot.DisplayStatus.State == "" || snapshot.WindowStatus.State == "" || snapshot.CoordinateSpace == "" {
		t.Fatalf("surface observation has incomplete status: %+v", snapshot)
	}
	for _, display := range snapshot.Displays {
		if display.ID == "" || display.Bounds.Width <= 0 || display.Bounds.Height <= 0 {
			t.Fatalf("invalid display: %+v", display)
		}
	}
	for _, window := range snapshot.Windows {
		if window.ID == "" || window.PID <= 0 || window.ProcessStartID == "" || window.Bounds.Width <= 0 || window.Bounds.Height <= 0 {
			t.Fatalf("invalid window: %+v", window)
		}
	}
}

func TestSurfaceObservationRoundTripsThroughHostGraph(t *testing.T) {
	ctx := context.Background()
	projected, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer projected.Close()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	statusTime := now
	snapshot := SurfaceSnapshot{
		Platform: "fixture", CoordinateSpace: "fixture-points", ObservedAt: now,
		Displays:      []DisplayInfo{{ID: "display:opaque", Bounds: SurfaceBounds{X: -10, Y: 0, Width: 1920, Height: 1080}, Scale: 2, Primary: true}},
		Windows:       []WindowInfo{{ID: "window:opaque", PID: 42, ProcessStartID: "42:100", Bounds: SurfaceBounds{X: 20, Y: 30, Width: 800, Height: 600}, OnScreen: true}},
		DisplayStatus: CollectionStatus{State: "complete", ObservedAt: &statusTime},
		WindowStatus:  CollectionStatus{State: "partial", Detail: "fixture limit", ObservedAt: &statusTime},
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
	if err := projected.ObserveHost(ctx, HostInventory{Surfaces: &SurfaceSnapshot{
		Platform: "fixture", CoordinateSpace: "fixture-points", ObservedAt: now,
		Displays: []DisplayInfo{}, Windows: []WindowInfo{},
		DisplayStatus: CollectionStatus{State: "complete", ObservedAt: &statusTime},
		WindowStatus:  CollectionStatus{State: "complete", ObservedAt: &statusTime},
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
