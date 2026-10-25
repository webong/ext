//go:build !cgo || (!linux && !darwin && !freebsd)

package nativego

import (
	"context"
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

func Supported() bool { return false }

func load(string) (func(context.Context) (plugin.Backend, error), error) {
	return nil, fmt.Errorf("%w: native Go loading requires cgo on Linux, macOS or FreeBSD", plugin.ErrUnsupported)
}
