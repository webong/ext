// Package inprocess binds a trusted endpoint without IPC. It supplies lifecycle
// cancellation, not process isolation. Handlers must honor context cancellation.
package inprocess

import (
	"context"
	"github.com/webong/ctx/pkg/plugin"
)

type Backend struct {
	endpoint plugin.Endpoint
	ctx      context.Context
	cancel   context.CancelFunc
}

func New(endpoint plugin.Endpoint) (*Backend, error) {
	if endpoint == nil {
		return nil, plugin.ErrInvalid
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Backend{endpoint, ctx, cancel}, nil
}
func (b *Backend) bound(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(b.ctx, cancel)
	if b.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}
func (b *Backend) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	ctx, cancel := b.bound(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return plugin.Descriptor{}, err
	}
	return b.endpoint.Handshake(ctx)
}
func (b *Backend) Invoke(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	ctx, cancel := b.bound(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return plugin.Response{}, err
	}
	return b.endpoint.Invoke(ctx, r)
}
func (b *Backend) Close() error { b.cancel(); return nil }
func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "inprocess", Protocols: []string{plugin.APIVersion}, Concurrent: true, Cancellation: "request", ProcessOwner: "host"}
}
