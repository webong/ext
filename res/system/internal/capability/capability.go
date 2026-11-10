// Package capability holds the one rule every collection status of res/system follows: a state and,
// where something is missing, why (testdata/capability/v1). Packages keep their own status types
// and error sentinels and ask this package whether a pair is allowed.
package capability

// Allowed reports whether state is known and reason fits it: none for complete and partial,
// permission or none for permission-denied, unsupported or tool_missing for unsupported, and none,
// permission, no_device or tool_missing for unavailable (a failed native read has no reason). The
// state exited, which only process observation uses, takes none.
func Allowed(state, reason string) bool {
	var allowed []string
	switch state {
	case "complete", "partial", "exited":
		allowed = []string{""}
	case "permission-denied":
		allowed = []string{"", "permission"}
	case "unsupported":
		allowed = []string{"unsupported", "tool_missing"}
	case "unavailable":
		allowed = []string{"", "permission", "no_device", "tool_missing"}
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
