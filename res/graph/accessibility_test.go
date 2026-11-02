package graph

import (
	"context"
	"testing"
	"time"

	"github.com/webong/ext/res/system/capture/surface"
	proc "github.com/webong/ext/res/system/service/process"
)

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
	accessibility := surface.AccessibilitySnapshot{
		Platform: "fixture", ObservedAt: now,
		Status: proc.CollectionStatus{State: "complete", ObservedAt: &statusTime},
		Elements: []surface.AccessibilityElement{{
			ID: "accessibility:opaque", PID: 42, ProcessStartID: "42:100", Role: "button",
			Identifier: "save", Actions: []string{"press"}, Enabled: &enabled,
		}},
	}
	permissions := surface.PermissionSnapshot{
		Platform: "fixture", ObservedAt: now,
		Accessibility:   surface.PermissionInfo{State: "ready", Scope: "fixture.accessibility"},
		ScreenRecording: surface.PermissionInfo{State: "denied", Scope: "fixture.recording"},
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
