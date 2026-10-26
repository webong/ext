package system

import (
	"context"
	"runtime"
	"time"
)

// PermissionInfo reports a native capability's current state without asking
// for access. State is ready, denied, not-determined, not-required,
// unsupported, or unavailable. Scope names the OS permission when one exists.
type PermissionInfo struct {
	State  string `json:"state"`
	Scope  string `json:"scope,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// PermissionSnapshot contains local OS permission state relevant to desktop
// observation. Discovery never opens settings or displays a native prompt.
type PermissionSnapshot struct {
	Platform        string         `json:"platform"`
	ObservedAt      time.Time      `json:"observed_at"`
	Accessibility   PermissionInfo `json:"accessibility"`
	ScreenRecording PermissionInfo `json:"screen_recording"`
}

// DiscoverPermissions reads current local permission state without requesting
// access or changing OS settings.
func DiscoverPermissions(ctx context.Context) (PermissionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return PermissionSnapshot{}, err
	}
	accessibility, recording, err := platformPermissions(ctx)
	if err != nil {
		return PermissionSnapshot{}, err
	}
	return PermissionSnapshot{
		Platform: runtime.GOOS, ObservedAt: time.Now().UTC(),
		Accessibility: accessibility, ScreenRecording: recording,
	}, ctx.Err()
}
