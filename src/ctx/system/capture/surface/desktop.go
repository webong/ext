package surface

import (
	"time"
)

const (
	DesktopNative    = "desktop.native"
	DesktopX11       = "desktop.x11"
	DesktopWayland   = "desktop.wayland"
	DesktopContainer = "desktop.container"
	DesktopRecording = "desktop.recording"
)

// DesktopEndpoint is evidence that a local desktop display endpoint is visible
// to this process. It never contains a socket path, credential, or remote host.
type DesktopEndpoint struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// RecordingInfo describes local recording prerequisites. Permission is an
// identifier for the embedding application to interpret; discovery never opens
// settings, requests permission, or starts a capture.
type RecordingInfo struct {
	Status           string    `json:"status"`
	EncoderAvailable bool      `json:"encoder_available"`
	CaptureAvailable bool      `json:"capture_available"`
	Permission       string    `json:"permission"`
	Formats          []string  `json:"formats,omitempty"`
	Message          string    `json:"message,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}

// DesktopInfo is a point-in-time observation of the native desktop visible to
// the current process. It does not describe a remote display or authorize use.
type DesktopInfo struct {
	Platform      string          `json:"platform"`
	Interactive   bool            `json:"interactive"`
	Containerized bool            `json:"containerized"`
	Capabilities  []string        `json:"capabilities,omitempty"`
	X11           DesktopEndpoint `json:"x11"`
	Wayland       DesktopEndpoint `json:"wayland"`
	Recording     RecordingInfo   `json:"recording"`
	Reason        string          `json:"reason,omitempty"`
	ObservedAt    time.Time       `json:"observed_at"`
}
