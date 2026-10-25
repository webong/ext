package c2goengine

import (
	"github.com/webong/ext/pkg/ext-go/c2goengine/internal/ccgo"
	"github.com/webong/ext/pkg/ext-go/engine/guest/instance"
)

type (
	// Instances binds typed Go resource factories to engine-owned leases, replacement, capacity and
	// cleanup, as cgoengine.Instances does, with the engine running as Go. Close frees the engine after
	// all outstanding leases and factories finish; a timed-out Close can be retried.
	Instances[T any] = ccgo.Instances[T]
	// InstanceLease holds an instance until Release.
	InstanceLease[T any] = ccgo.InstanceLease[T]
)

// NewInstances creates a manager of at most options.Capacity instances.
func NewInstances[T any](options instance.Options[T]) (*Instances[T], error) {
	return ccgo.NewInstances(options)
}
