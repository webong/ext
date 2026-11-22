// Package cguest runs plugin guests built as C shared libraries (lib/plugin/include/ext_plugin.h),
// including Go -buildmode=c-shared guests, as a plugin backend. cengine/cguest loads the library and
// exchanges bytes; this package speaks the plugin protocol over it: ext.plugin/v1 frames, the frame
// limit and default deadline, the descriptor and response checks, and the engine's errors. Native
// code executes inside the host and cannot be forcibly stopped.
package cguest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	raw "github.com/webong/ext/pkg/ext-go/cengine/cguest"
	"github.com/webong/ext/pkg/ext-go/cengine/cguest/abi"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// The ABI's frame limit is the plugin contract's.
var _ [abi.MaxFrameBytes - plugin.MaxFrameBytes]struct{}
var _ [plugin.MaxFrameBytes - abi.MaxFrameBytes]struct{}

// Supported reports whether this build can load a C ABI guest at all.
func Supported() bool { return raw.Supported() }

// Backend is one C ABI guest as a plugin.Backend.
type Backend struct{ guest *raw.Guest }

// Open loads an absolute verified library path, checks ABI v1 and creates an independent guest
// handle, within plugin.DefaultTimeout. Use inside plugin.Options.Connect. Library images remain
// resident for process lifetime, even after errors; use immutable revision-specific artifact paths.
func Open(ctx context.Context, path string) (*Backend, error) {
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
	g, err := raw.Open(ctx, path)
	if err != nil {
		return nil, engineError(err)
	}
	return &Backend{guest: g}, nil
}

func (b *Backend) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	data, err := b.exchange(ctx, abi.Handshake, abi.Hello{Deadline: deadline})
	var d plugin.Descriptor
	if err == nil {
		err = plugin.Decode(data, &d)
	}
	if err == nil {
		err = d.Validate()
	}
	if err != nil {
		_ = b.Close()
	}
	return d, err
}

func (b *Backend) Invoke(ctx context.Context, req plugin.Request) (plugin.Response, error) {
	if !req.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, req.Deadline)
		defer cancel()
	}
	data, err := b.exchange(ctx, abi.Invoke, req)
	var resp plugin.Response
	if err == nil {
		err = plugin.Decode(data, &resp)
	}
	if err == nil {
		err = resp.Validate(req.ID)
	}
	if err != nil {
		_ = b.Close()
	}
	return resp, err
}

func (b *Backend) exchange(ctx context.Context, op uint32, value any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out, err := b.guest.Call(ctx, op, data)
	return out, engineError(err)
}

// Close stops admission and releases waiting callers immediately. Native cleanup runs after an
// outstanding call returns. It never unloads the image.
func (b *Backend) Close() error { return b.guest.Close() }

// WaitClosed waits for actual guest resource cleanup after Close.
func (b *Backend) WaitClosed(ctx context.Context) error { return b.guest.WaitClosed(ctx) }

func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "cshared", Protocols: []string{plugin.APIVersion}, Cancellation: "detach-and-close-after-return", ProcessOwner: "host", Boundary: plugin.BoundaryInProcess}
}

// engineError gives a loader error the engine's error value as well, so callers see the same
// errors from every backend.
func engineError(err error) error {
	for _, m := range []struct{ raw, engine error }{
		{raw.ErrInvalid, plugin.ErrInvalid},
		{raw.ErrUnsupported, plugin.ErrUnsupported},
		{raw.ErrDenied, plugin.ErrDenied},
		{raw.ErrClosed, plugin.ErrClosed},
	} {
		if errors.Is(err, m.raw) {
			return fmt.Errorf("%w: %w", m.engine, err)
		}
	}
	return err
}
