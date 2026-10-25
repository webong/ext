package core

import (
	"context"
	"encoding/json"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/guest/instance"
	"github.com/webong/ext/pkg/plugin-go/engine/host/process"
)

// goEngine is the pure-Go engine: the oracle, and the engine that exists on every platform.
type goEngine struct{}

func (goEngine) sealed()         {}
func (goEngine) Name() Name      { return Go }
func (goEngine) Available() bool { return true }
func (goEngine) Features() Feature {
	return Sessions | Progress | Reverse
}

func (e goEngine) Call(string, json.RawMessage) (json.RawMessage, error) {
	return nil, errUnsupported(e, "services")
}

// goSession is a contract session; one opened over a child process also keeps the process for its
// stderr.
type goSession struct {
	*plugin.Session
	proc *process.Process
}

func (s goSession) Stderr() string {
	if s.proc == nil {
		return ""
	}
	return s.proc.Stderr()
}

func (goEngine) Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (Session, error) {
	s, err := plugin.Open(ctx, d, options)
	if err != nil {
		return nil, err
	}
	return session{Go, goSession{Session: s}}, nil
}

func (goEngine) OpenProcess(ctx context.Context, d plugin.Descriptor, launch Launch, options plugin.Options) (Session, error) {
	if launch.StderrBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	var proc *process.Process
	connect := options
	connect.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		p, err := process.Start(ctx, process.Command{
			Path:        launch.Executable,
			Args:        launch.Arguments,
			Dir:         launch.Directory,
			Env:         launch.Environment,
			StderrBytes: launch.StderrBytes,
		})
		if err != nil {
			return nil, err
		}
		proc = p
		return p, nil
	}
	s, err := plugin.Open(ctx, d, connect)
	if err != nil {
		return nil, err
	}
	return session{Go, goSession{Session: s, proc: proc}}, nil
}

type goInstances[T any] struct{ m *instance.Manager[T] }

func newGoInstances[T any](_ goEngine, options instance.Options[T]) (Instances[T], error) {
	m, err := instance.New(options)
	if err != nil {
		return nil, err
	}
	return goInstances[T]{m}, nil
}

func (g goInstances[T]) Configure(ctx context.Context, key, revision string, config json.RawMessage) error {
	return g.m.Configure(ctx, key, revision, config)
}
func (g goInstances[T]) Acquire(key string) (*Lease[T], error) {
	l, err := g.m.Acquire(key)
	if err != nil {
		return nil, err
	}
	return &Lease[T]{Value: l.Value, Revision: l.Revision, release: l.Release}, nil
}
func (g goInstances[T]) Remove(key string) error         { return g.m.Remove(key) }
func (g goInstances[T]) Close(ctx context.Context) error { return g.m.Close(ctx) }
