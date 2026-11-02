package surface

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
