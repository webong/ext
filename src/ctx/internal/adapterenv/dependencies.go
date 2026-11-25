// Package adapterenv prepares CTX-owned adapter invocation metadata.
package adapterenv

import (
	"sort"
	"strings"

	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
)

// Dependencies replaces inherited dependency hints with the selected manifest's
// declarations. These hints select providers; they do not grant trust. Nested
// calls must still perform their own admission. The input slice is not modified.
func Dependencies(values []string, declared map[string]adapter.Dependency) []string {
	result := make([]string, 0, len(values)+len(declared))
	for _, value := range values {
		if !strings.HasPrefix(strings.ToUpper(value), "EXT_DEPENDENCY_") {
			result = append(result, value)
		}
	}
	spaces := make([]string, 0, len(declared))
	for space := range declared {
		spaces = append(spaces, space)
	}
	sort.Strings(spaces)
	for _, space := range spaces {
		result = append(result, "EXT_DEPENDENCY_"+strings.ToUpper(space)+"="+declared[space].Adapter)
	}
	return result
}
