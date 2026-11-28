package surface

import (
	"fmt"
	"time"

	"github.com/webong/ext/src/ctx/system/internal/capability"
)

// CollectionStatus describes whether an observation is complete, partial, or
// unavailable. It is part of the surface contract, independent of the process
// collector that used to provide this type.
type CollectionStatus struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

func (s CollectionStatus) Validate() error {
	if !capability.Allowed(s.State, s.Reason) || s.State == "exited" {
		return fmt.Errorf("collection state %q does not take reason %q", s.State, s.Reason)
	}
	return nil
}
