//go:build ext_cengine && cgo && (darwin || linux)

package main

import (
	"context"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/transport/bridge"
)

func openRelay(ctx context.Context, d plugin.Descriptor, o plugin.Options) (*bridge.Relay, error) {
	session, err := cgoengine.Open(ctx, d, o)
	if err != nil {
		return nil, err
	}
	relay, err := bridge.New(session)
	if err != nil {
		_ = session.Abort()
	}
	return relay, err
}
