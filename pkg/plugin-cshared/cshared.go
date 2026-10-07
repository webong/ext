// Package cshared loads trusted C ABI libraries, including Go -buildmode=c-shared
// guests. Native code executes inside the host and cannot be forcibly stopped.
package cshared

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin-cshared/abi"
)

type nativeSession interface {
	call(uint32, []byte) ([]byte, error)
	close()
}

type Backend struct {
	native nativeSession
	gate   chan struct{}
	closed chan struct{}
	done   chan struct{}
	once   sync.Once
}

// Open loads an absolute verified library path, checks ABI v1 and creates an
// independent guest handle. Use inside plugin.Options.Connect. Library images
// remain resident for process lifetime, even after errors: dlclose is unsafe for
// Go runtimes and callbacks. Use immutable revision-specific artifact paths.
// Cancellation returns promptly but cannot stop native initializers or open;
// a handle returned later is closed by the outstanding startup goroutine.
func Open(ctx context.Context, path string) (*Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || !utf8.ValidString(path) {
		return nil, fmt.Errorf("%w: C shared plugin requires an absolute UTF-8 path", plugin.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, plugin.DefaultTimeout)
	defer cancel()
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
		b := &Backend{native: r.native, gate: make(chan struct{}, 1), closed: make(chan struct{}), done: make(chan struct{})}
		if err := ctx.Err(); err != nil {
			_ = b.Close()
			return nil, err
		}
		return b, nil
	}
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
	if len(data) > plugin.MaxFrameBytes {
		return nil, plugin.ErrInvalid
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.closed:
		return nil, plugin.ErrClosed
	case b.gate <- struct{}{}:
	}
	if err := ctx.Err(); err != nil {
		<-b.gate
		return nil, err
	}
	select {
	case <-b.closed:
		<-b.gate
		return nil, plugin.ErrClosed
	default:
	}
	type result struct {
		data []byte
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		defer func() { <-b.gate }()
		response, err := b.native.call(op, data)
		ready <- result{response, err}
	}()
	select {
	case <-ctx.Done():
		_ = b.Close()
		return nil, ctx.Err()
	case <-b.closed:
		return nil, plugin.ErrClosed
	case r := <-ready:
		if err := ctx.Err(); err != nil {
			_ = b.Close()
			return nil, err
		}
		if r.err != nil {
			_ = b.Close()
		}
		return r.data, r.err
	}
}

// Close stops admission and releases waiting callers immediately. Native
// cleanup runs after an outstanding call returns. It never unloads the image.
// Native code must honor request deadlines; if it hangs, cleanup stays pending.
func (b *Backend) Close() error {
	b.once.Do(func() {
		close(b.closed)
		go func() {
			b.gate <- struct{}{}
			b.native.close()
			<-b.gate
			close(b.done)
		}()
	})
	return nil
}

// WaitClosed waits for actual guest resource cleanup after Close.
func (b *Backend) WaitClosed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return nil
	}
}

func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "cshared", Protocols: []string{plugin.APIVersion}, Cancellation: "detach-and-close-after-return", ProcessOwner: "host"}
}
