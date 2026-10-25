//go:build !cgo || (!linux && !darwin && !freebsd && !windows)

package cshared

import (
	"fmt"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
)

func Supported() bool { return false }
func openNative(string) (nativeSession, error) {
	return nil, fmt.Errorf("%w: C shared loading requires cgo on Linux, macOS, FreeBSD or Windows", plugin.ErrUnsupported)
}
