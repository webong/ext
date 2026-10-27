package taskscheduler

import "strings"

// taskStatus reads the "Status" field of `schtasks /Query /FO LIST /V` output.
// Windows localizes the field name; an unrecognized status stays inactive and
// the raw output remains available in the status detail.
func taskStatus(output []byte) bool {
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Status") {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(value), "Running")
	}
	return false
}
