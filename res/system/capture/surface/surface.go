package surface

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/webong/ext/res/system/internal/capability"
	"github.com/webong/ext/res/system/internal/identity"
	proc "github.com/webong/ext/res/system/service/process"
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
	Platform        string                `json:"platform"`
	CoordinateSpace string                `json:"coordinate_space"`
	ObservedAt      time.Time             `json:"observed_at"`
	Displays        []DisplayInfo         `json:"displays"`
	Windows         []WindowInfo          `json:"windows"`
	DisplayStatus   proc.CollectionStatus `json:"display_status"`
	WindowStatus    proc.CollectionStatus `json:"window_status"`
}

// SurfaceOptions bounds returned metadata and collection time. In a Go literal
// a zero field is an absent one and selects the default: 64 displays, 4096
// windows, and a five-second deadline. Limits that arrive as data are explicit:
// Validate rejects a zero, as the other bindings do (testdata/surface/v1).
type SurfaceOptions struct {
	MaxDisplays int
	MaxWindows  int
	Timeout     time.Duration
}

// DefaultSurfaceOptions returns the default of every limit and the default timeout.
func DefaultSurfaceOptions() SurfaceOptions {
	return SurfaceOptions{MaxDisplays: 64, MaxWindows: 4096, Timeout: 5 * time.Second}
}

// Validate checks explicit options: every limit is between 1 and 65536, so a
// zero is invalid, and the timeout is not negative (zero selects five seconds).
func (o SurfaceOptions) Validate() error {
	if o.MaxDisplays < 1 || o.MaxDisplays > 65536 || o.MaxWindows < 1 || o.MaxWindows > 65536 {
		return fmt.Errorf("%w: surface limits must be between 1 and 65536", ErrInvalid)
	}
	if o.Timeout < 0 {
		return fmt.Errorf("%w: surface timeout must not be negative", ErrInvalid)
	}
	return nil
}

func surfaceOptions(options SurfaceOptions) (SurfaceOptions, error) {
	defaults := DefaultSurfaceOptions()
	if options.MaxDisplays == 0 {
		options.MaxDisplays = defaults.MaxDisplays
	}
	if options.MaxWindows == 0 {
		options.MaxWindows = defaults.MaxWindows
	}
	if options.Timeout == 0 {
		options.Timeout = defaults.Timeout
	}
	return options, options.Validate()
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

func stampSurfaceStatus(status *proc.CollectionStatus, observed time.Time) {
	if status.State == "" {
		status.State = "complete"
	}
	if status.ObservedAt == nil {
		value := observed
		status.ObservedAt = &value
	}
}

func surfaceStatus(state, detail string) proc.CollectionStatus {
	return proc.CollectionStatus{State: state, Detail: detail}
}

// surfaceUnavailable reports something this platform can observe but could not now. The reason
// comes from err, the failure of the native read: permission, tool_missing, or no_device when the
// display, session or application to read is absent or did not answer (a nil err).
func surfaceUnavailable(err error, detail string) proc.CollectionStatus {
	return proc.CollectionStatus{State: "unavailable", Reason: capability.Unavailable(err), Detail: detail}
}

// surfaceUnsupported reports a platform or build that cannot observe something at all.
func surfaceUnsupported(detail string) proc.CollectionStatus {
	return proc.CollectionStatus{State: "unsupported", Reason: "unsupported", Detail: detail}
}

func surfaceID(kind, native string) string {
	return kind + ":" + identity.Digest(kind+"\x00"+native)
}

func surfaceWindowID(pid int, startID, native string) string {
	return surfaceID("window", strconv.Itoa(pid)+"\x00"+startID+"\x00"+native)
}
