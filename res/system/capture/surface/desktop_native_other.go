//go:build (!darwin && !windows) || (darwin && !cgo)

package surface

import (
	"fmt"
	"runtime"
)

func newNativeDesktopBackend() (nativeDesktopBackend, error) {
	return nil, fmt.Errorf("%w: the native desktop driver needs macOS built with cgo or Windows, not %s", ErrDesktopUnavailable, runtime.GOOS)
}
