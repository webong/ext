//go:build !windows

package volume

import "os"

func broadenPermissionsForTest(path string) error { return os.Chmod(path, 0o755) }
