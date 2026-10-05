//go:build !cgo || (!linux && !darwin && !freebsd && !windows)

package cshared

import (
	"fmt"
	"github.com/webong/ctx/pkg/plugin"
)

func Supported() bool { return false }
func openNative(string) (nativeSession, error) {
	return nil, fmt.Errorf("%w: C shared loading requires cgo on Linux, macOS, FreeBSD or Windows", plugin.ErrUnsupported)
}
