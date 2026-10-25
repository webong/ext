//go:build !((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (linux && arm64))

package ccgo

import (
	"context"
	"encoding/json"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	instancepkg "github.com/webong/ext/pkg/ext-go/engine/guest/instance"
)

// HostAvailable is false where no translation of the host has been generated for the platform.
const HostAvailable = false

// Session is unavailable on this platform: every constructor reports plugin.ErrUnsupported.
type Session struct{}

func (*Session) Done() <-chan struct{}         { return nil }
func (*Session) Descriptor() plugin.Descriptor { return plugin.Descriptor{} }
func (*Session) State() plugin.State           { return plugin.StateClosed }
func (*Session) Stderr() string                { return "" }
func (*Session) Abort() error                  { return plugin.ErrUnsupported }
func (*Session) Close(context.Context) error   { return plugin.ErrUnsupported }
func (*Session) DrainContext(context.Context) error {
	return plugin.ErrUnsupported
}
func (*Session) Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error) {
	return nil, plugin.ErrUnsupported
}

// Open reports plugin.ErrUnsupported: this platform has no translation of the host.
func Open(context.Context, plugin.Descriptor, plugin.Options) (*Session, error) {
	return nil, plugin.ErrUnsupported
}

// OpenProcess reports plugin.ErrUnsupported: this platform has no translation of the host.
func OpenProcess(context.Context, plugin.Descriptor, LaunchOptions, plugin.Options) (*Session, error) {
	return nil, plugin.ErrUnsupported
}

// Instances is unavailable on this platform: NewInstances reports plugin.ErrUnsupported.
type Instances[T any] struct{}

// InstanceLease is unavailable on this platform.
type InstanceLease[T any] struct {
	Value    T
	Revision string
}

// NewInstances reports plugin.ErrUnsupported: this platform has no translation of the host.
func NewInstances[T any](instancepkg.Options[T]) (*Instances[T], error) {
	return nil, plugin.ErrUnsupported
}
func (*Instances[T]) Configure(context.Context, string, string, json.RawMessage) error {
	return plugin.ErrUnsupported
}
func (*Instances[T]) Acquire(string) (*InstanceLease[T], error) { return nil, plugin.ErrUnsupported }
func (*Instances[T]) Remove(string) error                       { return plugin.ErrUnsupported }
func (*Instances[T]) Close(context.Context) error               { return plugin.ErrUnsupported }
func (*InstanceLease[T]) Release() error                        { return plugin.ErrUnsupported }
