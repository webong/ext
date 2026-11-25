package plugin

import "fmt"

const DefaultProgressEvents = 50
const DefaultProgressBytes = 64 << 10

// ProgressLimits is the rate at which a call may report. Zero fields use the
// defaults. It travels in the request so both ends apply the same limits.
type ProgressLimits struct {
	EventsPerSecond int `json:"eventsPerSecond,omitempty"`
	BytesPerSecond  int `json:"bytesPerSecond,omitempty"`
}

// Resolved fills in defaults.
func (l ProgressLimits) Resolved() ProgressLimits {
	if l.EventsPerSecond <= 0 {
		l.EventsPerSecond = DefaultProgressEvents
	}
	if l.BytesPerSecond <= 0 {
		l.BytesPerSecond = DefaultProgressBytes
	}
	return l
}

// Validate bounds explicit limits.
func (l ProgressLimits) Validate() error {
	if l.EventsPerSecond < 0 || l.BytesPerSecond < 0 || l.EventsPerSecond > 1<<20 || l.BytesPerSecond > 1<<30 {
		return fmt.Errorf("%w: progress limits out of range", ErrInvalid)
	}
	return nil
}
