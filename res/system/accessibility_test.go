package system

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
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

func TestAccessibilityAndPermissionsRoundTripThroughHostGraph(t *testing.T) {
	ctx := context.Background()
	projected, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer projected.Close()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	statusTime := now
	enabled := true
	accessibility := AccessibilitySnapshot{
		Platform: "fixture", ObservedAt: now,
		Status: CollectionStatus{State: "complete", ObservedAt: &statusTime},
		Elements: []AccessibilityElement{{
			ID: "accessibility:opaque", PID: 42, ProcessStartID: "42:100", Role: "button",
			Identifier: "save", Actions: []string{"press"}, Enabled: &enabled,
		}},
	}
	permissions := PermissionSnapshot{
		Platform: "fixture", ObservedAt: now,
		Accessibility:   PermissionInfo{State: "ready", Scope: "fixture.accessibility"},
		ScreenRecording: PermissionInfo{State: "denied", Scope: "fixture.recording"},
	}
	if err := projected.ObserveHost(ctx, HostInventory{Accessibility: &accessibility, Permissions: &permissions}); err != nil {
		t.Fatal(err)
	}
	stored, err := projected.ReadHost(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Accessibility == nil || len(stored.Accessibility.Elements) != 1 || stored.Accessibility.Elements[0].Identifier != "save" {
		t.Fatalf("accessibility observation did not round trip: %+v", stored.Accessibility)
	}
	if stored.Permissions == nil || stored.Permissions.Accessibility.State != "ready" || stored.Permissions.ScreenRecording.State != "denied" {
		t.Fatalf("permission observation did not round trip: %+v", stored.Permissions)
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
