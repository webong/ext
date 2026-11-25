//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package plugin

import (
	"context"
	"errors"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	native "github.com/webong/ext/pkg/ext-go/cengine/plugin"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"sync"
)

// GuestRegistry routes reverse calls through the canonical C registry.
type GuestRegistry struct {
	once   sync.Once
	native *native.Registry
	err    error
}

func NewGuestRegistry() *GuestRegistry { return &GuestRegistry{} }
func (r *GuestRegistry) init() error {
	r.once.Do(func() {
		var l *native.Library
		l, r.err = native.Default()
		if r.err == nil {
			r.native, r.err = l.NewRegistry()
		}
	})
	return r.err
}
func registryError(err error) error {
	var code *library.CodeError
	if errors.As(err, &code) && code.Name == "exists" {
		return ErrRegistryExists
	}
	return err
}
func validName(s string) bool { return plugin.ContractRef{Name: s, Version: "v1"}.Validate() == nil }
func (r *GuestRegistry) Register(ref plugin.ContractRef, s *Session) error {
	if s == nil || s.Session == nil {
		return plugin.ErrInvalid
	}
	if err := r.init(); err != nil {
		return err
	}
	return registryError(r.native.Register(ref, s.Session))
}
func (r *GuestRegistry) Unregister(ref plugin.ContractRef) error {
	if err := r.init(); err != nil {
		return err
	}
	return registryError(r.native.Unregister(ref))
}
func (r *GuestRegistry) Route(from plugin.ContractRef, op string, to plugin.ContractRef, target string) error {
	if err := r.init(); err != nil {
		return err
	}
	return registryError(r.native.Route(from, op, to, target))
}
func (r *GuestRegistry) OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	s, err := r.native.OpenProcess(ctx, d, native.Launch{Executable: launch.Executable, Arguments: launch.Arguments, Environment: launch.Environment, Directory: launch.Directory, StderrBytes: launch.StderrBytes}, options)
	if err != nil {
		return nil, registryError(err)
	}
	return &Session{s}, nil
}
