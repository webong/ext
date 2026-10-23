//go:build !ext_cengine

package crosslang_test

import (
	"context"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
	"testing"
)

func runGuestConformance(t *testing.T, factory plugintest.Factory) { plugintest.Run(t, factory) }

// runFrames runs the frames suite on a guest process, through the pure-Go session.
func runFrames(t *testing.T, path string, opts plugintest.FramesOptions) {
	plugintest.RunFrames(t, func(context.Context) (plugin.Backend, error) { return process(path) }, opts)
}
