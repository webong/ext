package surface

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"time"

	proc "github.com/webong/ext/res/system/service/process"
	"strconv"
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
	Status     proc.CollectionStatus  `json:"status"`
	Elements   []AccessibilityElement `json:"elements"`
}

// AccessibilityOptions bounds traversal. Text is omitted by default because
// labels and values may contain document or form contents. Every bound is
// explicit and a zero is invalid, as in the other bindings
// (testdata/surface/v1); start from DefaultAccessibilityOptions (1024 elements,
// depth 16, five seconds) and change what differs.
type AccessibilityOptions struct {
	MaxElements int
	MaxDepth    int
	Timeout     time.Duration
	IncludeText bool
}

// DefaultAccessibilityOptions returns the default of every limit and the
// default timeout, without text.
func DefaultAccessibilityOptions() AccessibilityOptions {
	return AccessibilityOptions{MaxElements: 1024, MaxDepth: 16, Timeout: 5 * time.Second}
}

// Validate checks that the element limit is between 1 and 65536, the depth
// between 1 and 256, and the timeout positive.
func (o AccessibilityOptions) Validate() error {
	if o.MaxElements < 1 || o.MaxElements > 65536 || o.MaxDepth < 1 || o.MaxDepth > 256 {
		return fmt.Errorf("%w: accessibility limits are outside the supported range", ErrInvalid)
	}
	if o.Timeout <= 0 {
		return fmt.Errorf("%w: accessibility timeout must be positive", ErrInvalid)
	}
	return nil
}

// DiscoverAccessibility observes the focused local application's native UI
// structure. It does not request permission, mutate attributes, invoke actions,
// synthesize input, capture pixels, or communicate with another process over a
// transport.
func DiscoverAccessibility(ctx context.Context, options AccessibilityOptions) (AccessibilitySnapshot, error) {
	err := options.Validate()
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
	return surfaceID("accessibility", strconv.Itoa(pid)+"\x00"+startID+"\x00"+path)
}
