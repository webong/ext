//go:build !(cgo && (linux || darwin || freebsd || windows)) && !(!cgo && (darwin || linux || windows) && (amd64 || arm64))

package cshared

import (
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"runtime"
)

func Supported() bool { return false }
func openNative(string) (nativeSession, error) {
	return nil, fmt.Errorf("%w: C shared loading on %s/%s needs cgo; without cgo it is available on Linux, macOS and Windows (amd64, arm64)", plugin.ErrUnsupported, runtime.GOOS, runtime.GOARCH)
}
