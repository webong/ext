package surface

import (
	"fmt"
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
