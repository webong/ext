// Package recorder is a compatibility entry point to the native C recorder.
// New requires an explicitly opened C store; it never creates a Go recorder.
package recorder

import (
	"context"
	native "github.com/webong/ext/pkg/ext-go/cengine/ledger"
)

type Graph = native.Graph

const Namespace = native.Namespace

func New(ctx context.Context, store *native.NativeStore) (*Graph, error) {
	return native.NewGraph(ctx, store)
}
