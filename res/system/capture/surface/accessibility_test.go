package surface

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAccessibilityOptionsAreBounded(t *testing.T) {
	if _, err := DiscoverAccessibility(context.Background(), AccessibilityOptions{MaxDepth: -1}); err == nil {
		t.Fatal("negative accessibility depth was accepted")
	}
	if _, err := DiscoverAccessibility(context.Background(), AccessibilityOptions{MaxElements: 65537}); err == nil {
		t.Fatal("excessive accessibility element limit was accepted")
	}
}

func TestAccessibilityDefaultsDoNotExposeTextOrNativeHandles(t *testing.T) {
	snapshot, err := DiscoverAccessibility(context.Background(), AccessibilityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, forbidden := range []string{"native_handle", "window_id", "element_pointer"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("accessibility observation exposed %q: %s", forbidden, encoded)
		}
	}
	if snapshot.Status.State == "" {
		t.Fatalf("missing accessibility status: %+v", snapshot)
	}
	for _, element := range snapshot.Elements {
		if element.ID == "" || element.PID <= 0 || element.ProcessStartID == "" || element.Role == "" {
			t.Fatalf("invalid accessibility element: %+v", element)
		}
		if element.Label != "" || element.Value != "" {
			t.Fatalf("default accessibility observation included text: %+v", element)
		}
	}
}

func TestPermissionDiscoveryNeverReturnsEmptyStates(t *testing.T) {
	snapshot, err := DiscoverPermissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Platform == "" || snapshot.ObservedAt.IsZero() || snapshot.Accessibility.State == "" || snapshot.ScreenRecording.State == "" {
		t.Fatalf("incomplete permission observation: %+v", snapshot)
	}
}
