package surface

import (
	"errors"
	"fmt"
	"math"

	"github.com/webong/ext/res/system/internal/capability"
)

// ErrInvalid reports a snapshot or an option that breaks the rules of testdata/surface/v1.
var ErrInvalid = errors.New("invalid surface value")

// Code is the stable error code of err for another language to match: invalid. It is empty for an
// error this package does not define.
func Code(err error) string {
	if errors.Is(err, ErrInvalid) {
		return "invalid"
	}
	return ""
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

// Validate checks the bounds: width and height are positive.
func (b SurfaceBounds) Validate() error {
	if b.Width <= 0 || b.Height <= 0 {
		return invalid("bounds must have a positive width and height")
	}
	return nil
}

// Validate checks a snapshot against the rules of testdata/surface/v1: the platform, coordinate space
// and time are set; display and window ids are set and unique; bounds are positive; a scale is not
// negative; at most one display is primary; a window has a process; each status fits the capability
// rules.
func (s SurfaceSnapshot) Validate() error {
	if s.Platform == "" || s.CoordinateSpace == "" || s.ObservedAt.IsZero() {
		return invalid("platform, coordinate space and observation time are required")
	}
	ids := map[string]bool{}
	primary := 0
	for _, display := range s.Displays {
		if display.ID == "" || ids[display.ID] {
			return invalid("a display id is required and unique")
		}
		ids[display.ID] = true
		if err := display.Bounds.Validate(); err != nil {
			return err
		}
		if !(display.Scale >= 0) || math.IsInf(display.Scale, 0) {
			return invalid("a display scale must not be negative")
		}
		if display.Primary {
			primary++
		}
	}
	if primary > 1 {
		return invalid("at most one display is primary")
	}
	ids = map[string]bool{}
	for _, window := range s.Windows {
		if window.ID == "" || ids[window.ID] {
			return invalid("a window id is required and unique")
		}
		ids[window.ID] = true
		if window.PID < 1 {
			return invalid("a window needs a process")
		}
		if err := window.Bounds.Validate(); err != nil {
			return err
		}
	}
	for name, status := range map[string]interface{ Validate() error }{"display_status": s.DisplayStatus, "window_status": s.WindowStatus} {
		if err := status.Validate(); err != nil {
			return invalid("%s: %v", name, err)
		}
	}
	return nil
}

// Validate checks that the state is known and the reason fits it: none for ready, denied,
// not-determined and not-required, and the capability reasons for unsupported and unavailable.
func (p PermissionInfo) Validate() error {
	switch p.State {
	case "ready", "denied", "not-determined", "not-required":
		if p.Reason != "" {
			return invalid("state %q takes no reason", p.State)
		}
	case "unsupported", "unavailable":
		if !capability.Allowed(p.State, p.Reason) {
			return invalid("state %q does not take reason %q", p.State, p.Reason)
		}
	default:
		return invalid("unknown permission state %q", p.State)
	}
	return nil
}

// Validate checks the platform and time and both permissions.
func (s PermissionSnapshot) Validate() error {
	if s.Platform == "" || s.ObservedAt.IsZero() {
		return invalid("platform and observation time are required")
	}
	if err := s.Accessibility.Validate(); err != nil {
		return err
	}
	return s.ScreenRecording.Validate()
}

// Validate checks a snapshot against the rules of testdata/surface/v1: the platform and time are set,
// the status fits the capability rules, element ids are set and unique, a parent is another element of
// the snapshot without a cycle, a process is named, a role is set, bounds are positive and action
// names are not empty.
func (s AccessibilitySnapshot) Validate() error {
	if s.Platform == "" || s.ObservedAt.IsZero() {
		return invalid("platform and observation time are required")
	}
	if err := s.Status.Validate(); err != nil {
		return invalid("status: %v", err)
	}
	parents := map[string]string{}
	for _, element := range s.Elements {
		if element.ID == "" {
			return invalid("an element id is required")
		}
		if _, repeated := parents[element.ID]; repeated {
			return invalid("an element id is unique")
		}
		parents[element.ID] = element.ParentID
		if element.PID < 1 || element.Role == "" {
			return invalid("an element needs a process and a role")
		}
		if element.Bounds != nil {
			if err := element.Bounds.Validate(); err != nil {
				return err
			}
		}
		for _, action := range element.Actions {
			if action == "" {
				return invalid("an action name must not be empty")
			}
		}
	}
	for id, parent := range parents {
		for steps := 0; parent != ""; steps++ {
			next, found := parents[parent]
			if !found || parent == id || steps > len(parents) {
				return invalid("a parent is another element of the snapshot, without a cycle")
			}
			parent = next
		}
	}
	return nil
}
