//go:build ext_cengine && cgo && (darwin || linux || windows)

package core

import (
	"context"
	"encoding/json"

	"github.com/webong/ext/pkg/plugin-go/cgoengine"
	"github.com/webong/ext/pkg/plugin-go/engine/guest/instance"
)

// This file is the build that links the C engine; cgo_nocgo.go is every other build. The constraint is
// cgoengine's own: it serves Instances and EngineCall only here.

func cgoEngineCall(operation string, input json.RawMessage) (json.RawMessage, error) {
	return cgoengine.EngineCall(operation, input)
}

type cgoInstances[T any] struct{ m *cgoengine.Instances[T] }

func newCGoInstances[T any](e cgoEngine, options instance.Options[T]) (Instances[T], error) {
	m, err := cgoengine.NewInstances(options)
	if err != nil {
		return nil, err
	}
	return cgoInstances[T]{m}, nil
}

func (g cgoInstances[T]) Configure(ctx context.Context, key, revision string, config json.RawMessage) error {
	return g.m.Configure(ctx, key, revision, config)
}
func (g cgoInstances[T]) Acquire(key string) (*Lease[T], error) {
	l, err := g.m.Acquire(key)
	if err != nil {
		return nil, err
	}
	return &Lease[T]{Value: l.Value, Revision: l.Revision, release: l.Release}, nil
}
func (g cgoInstances[T]) Remove(key string) error         { return g.m.Remove(key) }
func (g cgoInstances[T]) Close(ctx context.Context) error { return g.m.Close(ctx) }
