package enginetest

import (
	"context"
	"encoding/json"

	"github.com/webong/ext/pkg/plugin-go/engine/guest/instance"
)

// Instances is the part of an instance manager that the checks use: the same calls on the
// oracle (instance.Manager) and on the engine under test.
type Instances interface {
	Configure(ctx context.Context, key, revision string, config json.RawMessage) error
	Remove(key string) error
	Close(ctx context.Context) error
	// Acquire leases the instance stored under key and returns its value.
	Acquire(key string) (string, Lease, error)
}

// Lease is held until Release; Release reports the error of disposing of a retired instance.
type Lease interface{ Release() error }

// oracleInstances is the pure-Go manager, the oracle.
type oracleInstances struct{ *instance.Manager[string] }

func (o oracleInstances) Acquire(key string) (string, Lease, error) {
	l, err := o.Manager.Acquire(key)
	if err != nil {
		return "", nil, err
	}
	return l.Value, l, nil
}
