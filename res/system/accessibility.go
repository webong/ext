package system

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"time"
)

// AccessibilityElement is a caller-visible node in the focused application's
// native accessibility tree. ID and ParentID are opaque snapshot identities;
// they are not native handles and cannot be used to invoke actions.
type AccessibilityElement struct {
	ID             string         `json:"id"`
	ParentID       string         `json:"parent_id,omitempty"`
	PID            int            `json:"pid"`
	ProcessStartID string         `json:"process_start_id"`
	Role           string         `json:"role"`
	Identifier     string         `json:"identifier,omitempty"`
	Label          string         `json:"label,omitempty"`
	Value          string         `json:"value,omitempty"`
	Bounds         *SurfaceBounds `json:"bounds,omitempty"`
	Actions        []string       `json:"actions,omitempty"`
	Enabled        *bool          `json:"enabled,omitempty"`
	Focused        *bool          `json:"focused,omitempty"`
}

// AccessibilitySnapshot is a bounded, read-only observation of the focused
// local application's UI tree. It never contains native handles or an action
// endpoint. Status distinguishes an empty tree from denied or unsupported
// native observation.
type AccessibilitySnapshot struct {
	Platform   string                 `json:"platform"`
	ObservedAt time.Time              `json:"observed_at"`
	Status     CollectionStatus       `json:"status"`
	Elements   []AccessibilityElement `json:"elements"`
}

// AccessibilityOptions bounds traversal. Text is omitted by default because
// labels and values may contain document or form contents. Zero bounds select
// 1024 elements, depth 16, and a five-second deadline.
type AccessibilityOptions struct {
	MaxElements int
	MaxDepth    int
	Timeout     time.Duration
	IncludeText bool
}

func accessibilityOptions(options AccessibilityOptions) (AccessibilityOptions, error) {
	if options.MaxElements < 0 || options.MaxDepth < 0 || options.Timeout < 0 {
		return options, errors.New("accessibility limits and timeout must not be negative")
	}
	if options.MaxElements == 0 {
		options.MaxElements = 1024
	}
	if options.MaxDepth == 0 {
		options.MaxDepth = 16
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.MaxElements > 65536 || options.MaxDepth > 256 {
		return options, errors.New("accessibility limits exceed the supported ceiling")
	}
	return options, nil
}

// DiscoverAccessibility observes the focused local application's native UI
// structure. It does not request permission, mutate attributes, invoke actions,
// synthesize input, capture pixels, or communicate with another process over a
// transport.
func DiscoverAccessibility(ctx context.Context, options AccessibilityOptions) (AccessibilitySnapshot, error) {
	options, err := accessibilityOptions(options)
	if err != nil {
		return AccessibilitySnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	now := time.Now().UTC()
	result := AccessibilitySnapshot{Platform: runtime.GOOS, ObservedAt: now, Elements: []AccessibilityElement{}}
	result.Elements, result.Status, err = platformAccessibility(ctx, options)
	if err != nil {
		return result, err
	}
	if result.Elements == nil {
		result.Elements = []AccessibilityElement{}
	}
	stampSurfaceStatus(&result.Status, now)
	for index := range result.Elements {
		sort.Strings(result.Elements[index].Actions)
	}
	sort.SliceStable(result.Elements, func(i, j int) bool { return result.Elements[i].ID < result.Elements[j].ID })
	return result, ctx.Err()
}

func accessibilityElementID(pid int, startID, path string) string {
	return surfaceID("accessibility", itoa(pid)+"\x00"+startID+"\x00"+path)
}
