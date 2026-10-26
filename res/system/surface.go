package system

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"strconv"
	"time"
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

// SurfaceOptions bounds returned metadata and collection time. Zero values
// select 64 displays, 4096 windows, and a five-second deadline.
type SurfaceOptions struct {
	MaxDisplays int
	MaxWindows  int
	Timeout     time.Duration
}

func surfaceOptions(options SurfaceOptions) (SurfaceOptions, error) {
	if options.MaxDisplays < 0 || options.MaxWindows < 0 || options.Timeout < 0 {
		return options, errors.New("surface limits and timeout must not be negative")
	}
	if options.MaxDisplays == 0 {
		options.MaxDisplays = 64
	}
	if options.MaxWindows == 0 {
		options.MaxWindows = 4096
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.MaxDisplays > 65536 || options.MaxWindows > 65536 {
		return options, errors.New("surface limits must not exceed 65536")
	}
	return options, nil
}

// DiscoverSurfaces observes local display geometry and caller-visible windows.
// It does not request permission, capture pixels, read window titles, expose
// native handles, synthesize input, or contact a remote service.
func DiscoverSurfaces(ctx context.Context, options SurfaceOptions) (SurfaceSnapshot, error) {
	options, err := surfaceOptions(options)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	now := time.Now().UTC()
	result := SurfaceSnapshot{
		Platform: runtime.GOOS, ObservedAt: now,
		Displays: []DisplayInfo{}, Windows: []WindowInfo{},
	}
	result.Displays, result.DisplayStatus, result.Windows, result.WindowStatus, result.CoordinateSpace, err =
		platformSurfaces(ctx, options.MaxDisplays, options.MaxWindows)
	if err != nil {
		return result, err
	}
	if result.Displays == nil {
		result.Displays = []DisplayInfo{}
	}
	if result.Windows == nil {
		result.Windows = []WindowInfo{}
	}
	stampSurfaceStatus(&result.DisplayStatus, now)
	stampSurfaceStatus(&result.WindowStatus, now)
	sort.Slice(result.Displays, func(i, j int) bool { return result.Displays[i].ID < result.Displays[j].ID })
	sort.Slice(result.Windows, func(i, j int) bool { return result.Windows[i].ID < result.Windows[j].ID })
	return result, ctx.Err()
}

func stampSurfaceStatus(status *CollectionStatus, observed time.Time) {
	if status.State == "" {
		status.State = "complete"
	}
	if status.ObservedAt == nil {
		value := observed
		status.ObservedAt = &value
	}
}

func surfaceStatus(state, detail string) CollectionStatus {
	return CollectionStatus{State: state, Detail: detail}
}

func surfaceID(kind, native string) string {
	return kind + ":" + digest(kind+"\x00"+native)
}

func surfaceWindowID(pid int, startID, native string) string {
	return surfaceID("window", strconv.Itoa(pid)+"\x00"+startID+"\x00"+native)
}
