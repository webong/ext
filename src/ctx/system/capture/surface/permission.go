package surface

import (
	"time"
)

// PermissionInfo reports a native capability's current state without asking
// for access. State is ready, denied, not-determined, not-required,
// unsupported, or unavailable. Scope names the OS permission when one exists.
type PermissionInfo struct {
	State string `json:"state"`
	// Reason says why something is unsupported or unavailable, from the set every package
	// shares (testdata/capability/v1): unsupported, permission, no_device or tool_missing.
	Reason string `json:"reason,omitempty"`
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
