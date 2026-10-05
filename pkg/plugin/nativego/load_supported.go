//go:build cgo && (linux || darwin || freebsd)

package nativego

import (
	"context"
	"fmt"
	stdplugin "plugin"

	"github.com/webong/ctx/pkg/plugin"
)

func Supported() bool { return true }

func load(path string) (func(context.Context) (plugin.Backend, error), error) {
	image, err := stdplugin.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load Go plugin: %w", err)
	}
	symbol, err := image.Lookup(Symbol)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", plugin.ErrUnsupported, Symbol, err)
	}
	factory, ok := symbol.(func(context.Context) (plugin.Backend, error))
	if !ok {
		return nil, fmt.Errorf("%w: %s must be func(context.Context) (plugin.Backend, error)", plugin.ErrUnsupported, Symbol)
	}
	return factory, nil
}
