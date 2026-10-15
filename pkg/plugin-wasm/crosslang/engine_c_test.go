//go:build ext_cengine && cgo && (darwin || linux)

package crosslang_test

import (
	"context"
	goengine "github.com/webong/ext/pkg/plugin-go"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/plugintest"
	"testing"
)

func runGuestConformance(t *testing.T, factory plugintest.Factory) {
	plugintest.RunWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.Host, error) {
		return goengine.Open(ctx, d, o)
	})
}
