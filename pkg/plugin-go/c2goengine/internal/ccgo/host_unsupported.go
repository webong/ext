//go:build !(darwin && arm64)

package ccgo

import (
	"context"
	"encoding/json"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
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
