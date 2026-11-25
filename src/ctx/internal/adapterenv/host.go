package adapterenv

import (
	"os"
	"strings"
)

// Host supplies CTX's dispatcher for nested calls. A socket product reaches its
// commands through ext; an embedded/legacy direct caller uses its own executable.
func Host(values []string) []string {
	executable := os.Getenv("EXT_HOST_EXECUTABLE")
	prefix := ""
	if executable != "" {
		prefix = "ctx"
	} else {
		executable, _ = os.Executable()
	}
	result := make([]string, 0, len(values)+2)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(key, "EXT_EXECUTABLE") && !strings.EqualFold(key, "EXT_PRODUCT_PREFIX") {
			result = append(result, value)
		}
	}
	if executable != "" {
		result = append(result, "EXT_EXECUTABLE="+executable)
	}
	if prefix != "" {
		result = append(result, "EXT_PRODUCT_PREFIX="+prefix)
	}
	return result
}
