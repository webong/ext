package surface

import (
	"fmt"
	"strconv"
	"time"

	"github.com/webong/ext/res/system/internal/identity"
)

// SurfaceBounds is a rectangle in the snapshot's coordinate space. Width and
// height are always positive. Coordinates may be negative on multi-display
// desktops.
type SurfaceBounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// DisplayInfo describes a display visible to the current process. ID is an
// opaque observation identity, not a native handle.
type DisplayInfo struct {
	ID      string        `json:"id"`
	Bounds  SurfaceBounds `json:"bounds"`
	Scale   float64       `json:"scale,omitempty"`
	Primary bool          `json:"primary"`
}

// WindowInfo describes an on-screen top-level window. It intentionally omits
// titles, application names, native handles, pixels, and input endpoints.
// ProcessStartID binds PID to a process generation when the OS exposes one.
type WindowInfo struct {
	ID             string        `json:"id"`
	PID            int           `json:"pid"`
	ProcessStartID string        `json:"process_start_id"`
	Bounds         SurfaceBounds `json:"bounds"`
	OnScreen       bool          `json:"on_screen"`
}

// SurfaceSnapshot is a bounded, non-atomic observation of local displays and
// windows. Separate statuses distinguish an empty desktop from unavailable or
// truncated native data.
type SurfaceSnapshot struct {
	Platform        string           `json:"platform"`
	CoordinateSpace string           `json:"coordinate_space"`
	ObservedAt      time.Time        `json:"observed_at"`
	Displays        []DisplayInfo    `json:"displays"`
	Windows         []WindowInfo     `json:"windows"`
	DisplayStatus   CollectionStatus `json:"display_status"`
	WindowStatus    CollectionStatus `json:"window_status"`
}

// SurfaceOptions bounds returned metadata and collection time. Every field is
// explicit and a zero is invalid, as in the other bindings
// (testdata/surface/v1); start from DefaultSurfaceOptions (64 displays, 4096
// windows, five seconds) and change what differs.
type SurfaceOptions struct {
	MaxDisplays int
	MaxWindows  int
	Timeout     time.Duration
}

// DefaultSurfaceOptions returns the default of every limit and the default timeout.
func DefaultSurfaceOptions() SurfaceOptions {
	return SurfaceOptions{MaxDisplays: 64, MaxWindows: 4096, Timeout: 5 * time.Second}
}

// Validate checks that every limit is between 1 and 65536 and that the
// timeout is positive.
func (o SurfaceOptions) Validate() error {
	if o.MaxDisplays < 1 || o.MaxDisplays > 65536 || o.MaxWindows < 1 || o.MaxWindows > 65536 {
		return fmt.Errorf("%w: surface limits must be between 1 and 65536", ErrInvalid)
	}
	if o.Timeout <= 0 {
		return fmt.Errorf("%w: surface timeout must be positive", ErrInvalid)
	}
	return nil
}

func surfaceID(kind, native string) string {
	return kind + ":" + identity.Digest(kind+"\x00"+native)
}

func surfaceWindowID(pid int, startID, native string) string {
	return surfaceID("window", strconv.Itoa(pid)+"\x00"+startID+"\x00"+native)
}
