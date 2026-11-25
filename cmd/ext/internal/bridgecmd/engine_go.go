//go:build !ext_cengine || !cgo || (!darwin && !linux)

package bridgecmd

import (
	"context"
	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/bridge"
)

func openRelay(ctx context.Context, d plugin.Descriptor, o plugin.Options) (*bridge.Relay, error) {
	s, err := plugin.Open(ctx, d, o)
	if err != nil {
		return nil, err
	}
	return bridge.New(s)
}
