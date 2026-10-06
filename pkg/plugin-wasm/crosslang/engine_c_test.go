//go:build ctx_cengine && cgo && (darwin || linux)

package crosslang_test

import (
	"context"
	goengine "github.com/webong/ctx/pkg/plugin-go"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/plugintest"
	"testing"
)

func runGuestConformance(t *testing.T, factory plugintest.Factory) {
	plugintest.RunWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.Host, error) {
		return goengine.Open(ctx, d, o)
	})
}
