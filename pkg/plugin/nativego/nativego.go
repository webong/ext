// Package nativego loads trusted Go shared objects built with -buildmode=plugin.
// Host and guest must use matching Go toolchains, flags and shared dependencies.
// Loading runs package initializers. Images remain loaded for the host lifetime.
package nativego

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/inprocess"
)

// Symbol is the required exported function name. The guest declares:
//
//	func CTXPlugin(context.Context) (plugin.Backend, error)
//
// Each call must create an independent backend. Its Close releases resources;
// it cannot unload code. The factory context bounds startup, not guest lifetime.
const Symbol = "CTXPlugin"

type Backend struct {
	*inprocess.Backend
	guest plugin.Backend
	once  sync.Once
	err   error
}

// Open loads an absolute, host-verified path and calls its exported factory.
// Use inside plugin.Options.Connect so verification precedes code execution.
// Go's loader and package initializers cannot be interrupted; factories and
// handlers must cooperate with context cancellation. Close must return promptly.
func Open(ctx context.Context, path string) (*Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf("%w: native Go plugin requires an absolute path", plugin.ErrInvalid)
	}
	factory, err := load(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	guest, err := factory(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if guest != nil {
			_ = guest.Close()
		}
		return nil, err
	}
	if guest == nil {
		return nil, plugin.ErrInvalid
	}
	bound, err := inprocess.New(guest)
	if err != nil {
		_ = guest.Close()
		return nil, err
	}
	return &Backend{Backend: bound, guest: guest}, nil
}

func (b *Backend) Close() error {
	b.once.Do(func() { _ = b.Backend.Close(); b.err = b.guest.Close() })
	return b.err
}

func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "nativego", Protocols: []string{plugin.APIVersion}, Concurrent: true, Cancellation: "cooperative-request", ProcessOwner: "host"}
}
