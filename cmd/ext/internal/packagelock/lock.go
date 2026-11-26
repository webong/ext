// Package packagelock coordinates CLI package mutations across processes.
// Library consumers must arrange their own transaction coordination.
package packagelock

import (
	"fmt"
	"path/filepath"

	"github.com/webong/ext/res/system/device/dirlock"
)

// Acquire fails immediately while another writer owns the home. A crashed
// writer leaves a lock: never guess that it is safe to remove based on age.
func Acquire(home string) (func(), error) {
	release, err := dirlock.Acquire(filepath.Join(home, ".package-write.lock"))
	if err != nil {
		return nil, fmt.Errorf("package %w", err)
	}
	return func() { _ = release() }, nil
}
