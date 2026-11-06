//go:build ext_cengine && cgo && (darwin || linux)

package crosslang_test

import (
	"context"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
	"testing"
)

// runFrames runs the frames suite on a guest through the pure-Go session and through
// the C engine, over the backend the factory makes and, for a guest that is a process
// (path is not empty), with the engine owning that process.
func runFrames(t *testing.T, factory plugintest.Factory, path string, opts plugintest.FramesOptions) {
	t.Run("Go session", func(t *testing.T) { plugintest.RunFrames(t, factory, opts) })
	t.Run("C engine, Go client", func(t *testing.T) {
		plugintest.RunFramesWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.FramesHost, error) {
			return cgoengine.Open(ctx, d, o)
		}, opts)
	})
	if path == "" {
		return
	}
	t.Run("C engine, engine-owned process", func(t *testing.T) {
		plugintest.RunFramesWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.FramesHost, error) {
			return cgoengine.OpenProcess(ctx, d, cgoengine.LaunchOptions{ProcessOptions: cgoengine.ProcessOptions{Executable: path}}, o)
		}, opts)
	})
}

func runGuestConformance(t *testing.T, factory plugintest.Factory) {
	plugintest.RunWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.Host, error) {
		return cgoengine.Open(ctx, d, o)
	})
}
