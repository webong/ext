// Package cguest loads trusted C ABI libraries (lib/plugin/include/ext_plugin_guest.h), including Go
// -buildmode=c-shared guests, and exchanges frames with them as bytes. It knows nothing of the
// plugin protocol: what the frames mean, and the limits and deadlines a session applies, belong to
// the caller (cguest/backend makes it a plugin backend). Native code executes inside the host
// and cannot be forcibly stopped.
//
// It loads with cgo on Linux, macOS, FreeBSD and Windows, and without cgo through purego on Linux,
// macOS and Windows for amd64 and arm64; Supported reports whether this build can load at all. The
// C ABI has no host callbacks, so both loaders implement all of it.
package cguest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/webong/ext/pkg/ext-go/cengine/cguest/abi"
)

var (
	// ErrInvalid is a bad path, an oversized frame or a call the guest reported as invalid.
	ErrInvalid = errors.New("cguest: invalid")
	// ErrUnsupported is a library this build cannot load or that is not an ABI v1 guest.
	ErrUnsupported = errors.New("cguest: unsupported")
	// ErrDenied is a guest that refused a new handle.
	ErrDenied = errors.New("cguest: denied")
	// ErrClosed is a call on a closed guest.
	ErrClosed = errors.New("cguest: closed")
)

type nativeSession interface {
	call(uint32, []byte) ([]byte, error)
	close()
}

// Guest is one handle on a loaded C ABI library. It runs one call at a time.
type Guest struct {
	native nativeSession
	gate   chan struct{}
	closed chan struct{}
	done   chan struct{}
	once   sync.Once
}

// Open loads an absolute verified library path, checks ABI v1 and creates an independent guest
// handle. Library images remain resident for process lifetime, even after errors: dlclose is unsafe
// for Go runtimes and callbacks. Use immutable revision-specific artifact paths. Cancellation returns
// promptly but cannot stop native initializers or open; a handle returned later is closed by the
// outstanding startup goroutine.
func Open(ctx context.Context, path string) (*Guest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || !utf8.ValidString(path) {
		return nil, fmt.Errorf("%w: a C ABI guest needs an absolute UTF-8 path", ErrInvalid)
	}
	type result struct {
		native nativeSession
		err    error
	}
	ready := make(chan result)
	go func() {
		native, err := openNative(path)
		select {
		case ready <- result{native, err}:
		case <-ctx.Done():
			if native != nil {
				native.close()
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ready:
		if r.err != nil {
			return nil, r.err
		}
		g := &Guest{native: r.native, gate: make(chan struct{}, 1), closed: make(chan struct{}), done: make(chan struct{})}
		if err := ctx.Err(); err != nil {
			_ = g.Close()
			return nil, err
		}
		return g, nil
	}
}

// Call sends one frame for operation op (abi.Handshake or abi.Invoke) and returns the guest's frame.
// A frame larger than abi.MaxFrameBytes is ErrInvalid. When ctx ends first, or the guest fails the
// call, the guest is closed.
func (g *Guest) Call(ctx context.Context, op uint32, data []byte) ([]byte, error) {
	if len(data) > abi.MaxFrameBytes {
		return nil, ErrInvalid
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.closed:
		return nil, ErrClosed
	case g.gate <- struct{}{}:
	}
	if err := ctx.Err(); err != nil {
		<-g.gate
		return nil, err
	}
	select {
	case <-g.closed:
		<-g.gate
		return nil, ErrClosed
	default:
	}
	type result struct {
		data []byte
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		defer func() { <-g.gate }()
		response, err := g.native.call(op, data)
		ready <- result{response, err}
	}()
	select {
	case <-ctx.Done():
		_ = g.Close()
		return nil, ctx.Err()
	case <-g.closed:
		return nil, ErrClosed
	case r := <-ready:
		if err := ctx.Err(); err != nil {
			_ = g.Close()
			return nil, err
		}
		if r.err != nil {
			_ = g.Close()
		}
		return r.data, r.err
	}
}

// Close stops admission and releases waiting callers immediately. Native cleanup runs after an
// outstanding call returns. It never unloads the image. Native code must honor request deadlines; if
// it hangs, cleanup stays pending.
func (g *Guest) Close() error {
	g.once.Do(func() {
		close(g.closed)
		go func() {
			g.gate <- struct{}{}
			g.native.close()
			<-g.gate
			close(g.done)
		}()
	})
	return nil
}

// WaitClosed waits for actual guest resource cleanup after Close.
func (g *Guest) WaitClosed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.done:
		return nil
	}
}
