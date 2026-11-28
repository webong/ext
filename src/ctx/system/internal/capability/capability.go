// Package capability holds the one rule every collection status of src/ctx/system follows: a state and,
// where something is missing, why (testdata/capability/v1). Packages keep their own status types
// and error sentinels and ask this package whether a pair is allowed.
package capability

import (
	"errors"
	"io/fs"
	"os/exec"
)

// The reasons every package shares (testdata/capability/v1).
const (
	Unsupported = "unsupported"
	Permission  = "permission"
	NoDevice    = "no_device"
	ToolMissing = "tool_missing"
)

// Allowed reports whether state is known and reason fits it: none for complete and partial,
// permission or none for permission-denied, unsupported or tool_missing for unsupported, and none,
// permission, no_device or tool_missing for unavailable. The state exited, which only process
// observation uses, takes none. The Go observers always give an unavailable status a reason
// (Unavailable); the empty one stays allowed for a status another binding wrote.
func Allowed(state, reason string) bool {
	var allowed []string
	switch state {
	case "complete", "partial", "exited":
		allowed = []string{""}
	case "permission-denied":
		allowed = []string{"", Permission}
	case "unsupported":
		allowed = []string{Unsupported, ToolMissing}
	case "unavailable":
		allowed = []string{"", Permission, NoDevice, ToolMissing}
	default:
		return false
	}
	for _, candidate := range allowed {
		if reason == candidate {
			return true
		}
	}
	return false
}

// Unavailable is the reason of an unavailable status after a native read failed with err:
// permission when the system refused the read, tool_missing when a program it needs is not
// installed, and no_device otherwise, which covers a device, session or source that is absent or
// did not answer. A nil err, for a read that failed without one, is no_device.
func Unavailable(err error) string {
	switch {
	case err == nil:
		return NoDevice
	case errors.Is(err, fs.ErrPermission):
		return Permission
	case errors.Is(err, exec.ErrNotFound):
		return ToolMissing
	}
	return NoDevice
}
