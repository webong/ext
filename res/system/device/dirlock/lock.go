// Package dirlock coordinates cooperating processes with an exclusive directory.
// It does not recover stale locks: callers must establish that the prior owner
// has stopped before removing one. All participants must use the same path.
package dirlock

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Acquire creates path exclusively, creating its parent directories if needed.
// It fails immediately on contention. The returned release is idempotent and
// refuses to remove a directory whose identity changed since acquisition.
// A process crash leaves the directory in place; no age-based expiry is used.
func Acquire(path string) (func() error, error) {
	var err error
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, fmt.Errorf("writer unavailable at %s (another writer or stale lock; remove only after confirming no writer is running): %w", path, err)
	}
	owned, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			current, err := os.Lstat(path)
			if err != nil {
				releaseErr = err
				return
			}
			if !os.SameFile(owned, current) {
				releaseErr = fmt.Errorf("lock ownership changed at %s", path)
				return
			}
			releaseErr = os.Remove(path)
		})
		return releaseErr
	}, nil
}
