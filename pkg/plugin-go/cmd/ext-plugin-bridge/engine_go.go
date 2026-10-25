//go:build !ext_cengine || !cgo || (!darwin && !linux)

package main

import (
	"context"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/transport/bridge"
)

func openRelay(ctx context.Context, d plugin.Descriptor, o plugin.Options) (*bridge.Relay, error) {
	return bridge.Open(ctx, d, o)
}
