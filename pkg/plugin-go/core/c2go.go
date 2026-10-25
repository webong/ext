package core

import (
	"context"
	"encoding/json"

	"github.com/webong/ext/pkg/plugin-go/c2goengine"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
)

// c2goEngine is the C engine translated to Go. It has the services wherever it was translated, and
// sessions where the host was translated too (not on Windows). It carries neither progress events
// nor reverse calls yet: a plugin that declares them is refused.
type c2goEngine struct{}

func (c2goEngine) sealed()         {}
func (c2goEngine) Name() Name      { return C2Go }
func (c2goEngine) Available() bool { return c2goengine.Available }
func (e c2goEngine) Features() Feature {
	if !e.Available() {
		return 0
	}
	f := Services
	if c2goengine.HostAvailable {
		f |= Sessions
	}
	return f
}

func (e c2goEngine) Call(operation string, input json.RawMessage) (json.RawMessage, error) {
	if !e.Available() {
		return nil, errUnsupported(e, "services")
	}
	return c2goengine.EngineCall(operation, input)
}

func (e c2goEngine) Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (Session, error) {
	if !e.Features().Has(Sessions) {
		return nil, errUnsupported(e, "sessions")
	}
	s, err := c2goengine.Open(ctx, d, options)
	if err != nil {
		return nil, err
	}
	return session{C2Go, s}, nil
}

func (e c2goEngine) OpenProcess(ctx context.Context, d plugin.Descriptor, launch Launch, options plugin.Options) (Session, error) {
	if !e.Features().Has(Sessions) {
		return nil, errUnsupported(e, "sessions")
	}
	s, err := c2goengine.OpenProcess(ctx, d, c2goengine.LaunchOptions{
		ProcessOptions: c2goengine.ProcessOptions{Executable: launch.Executable, Arguments: launch.Arguments, Environment: launch.Environment},
		Directory:      launch.Directory,
		StderrBytes:    launch.StderrBytes,
	}, options)
	if err != nil {
		return nil, err
	}
	return session{C2Go, s}, nil
}

type c2goInstances[T any] struct{ m *c2goengine.Instances[T] }

func newC2GoInstances[T any](e c2goEngine, options instance.Options[T]) (Instances[T], error) {
	if !e.Features().Has(Sessions) {
		return nil, errUnsupported(e, "sessions")
	}
	m, err := c2goengine.NewInstances(options)
	if err != nil {
		return nil, err
	}
	return c2goInstances[T]{m}, nil
}

func (g c2goInstances[T]) Configure(ctx context.Context, key, revision string, config json.RawMessage) error {
	return g.m.Configure(ctx, key, revision, config)
}
func (g c2goInstances[T]) Acquire(key string) (*Lease[T], error) {
	l, err := g.m.Acquire(key)
	if err != nil {
		return nil, err
	}
	return &Lease[T]{Value: l.Value, Revision: l.Revision, release: l.Release}, nil
}
func (g c2goInstances[T]) Remove(key string) error         { return g.m.Remove(key) }
func (g c2goInstances[T]) Close(ctx context.Context) error { return g.m.Close(ctx) }
