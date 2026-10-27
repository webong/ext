//go:build darwin

package volume

import (
	"path/filepath"
)

// systemAlias allows macOS's traditional absolute paths, which the system
// intentionally exposes as links into /private. They are not caller-controlled
// path components; a link anywhere below an admitted path is still rejected.
func systemAlias(path string) bool {
	switch filepath.Clean(path) {
	case "/var", "/tmp", "/etc":
		resolved, err := filepath.EvalSymlinks(path)
		return err == nil && filepath.Clean(resolved) == filepath.Join("/private", filepath.Clean(path))
	}
	return false
}
