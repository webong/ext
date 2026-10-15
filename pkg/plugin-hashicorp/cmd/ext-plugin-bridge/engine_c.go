//go:build ext_cengine && cgo && (darwin || linux)

package main

import (
	"context"
	goengine "github.com/webong/ext/pkg/plugin-go"
	"github.com/webong/ext/pkg/plugin-go/bridge"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

func openRelay(ctx context.Context, d plugin.Descriptor, o plugin.Options) (*bridge.Relay, error) {
	session, err := goengine.Open(ctx, d, o)
	if err != nil {
		return nil, err
	}
	relay, err := bridge.New(session)
	if err != nil {
		_ = session.Abort()
	}
	return relay, err
}
