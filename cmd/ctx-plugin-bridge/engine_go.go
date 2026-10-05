//go:build !ctx_cengine || !cgo || (!darwin && !linux)

package main

import (
	"context"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/bridge"
)

func openRelay(ctx context.Context, d plugin.Descriptor, o plugin.Options) (*bridge.Relay, error) {
	return bridge.Open(ctx, d, o)
}
