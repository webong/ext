//go:build ext_cengine && cgo && (darwin || linux)

package crosslang_test

import (
	"context"
	goengine "github.com/webong/ext/pkg/plugin-go/cengine"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
	"testing"
)

// runFrames runs the frames suite on a guest process through the pure-Go session
// and through the C engine, both when a Go client reads the process and when the
// engine owns it.
func runFrames(t *testing.T, path string, opts plugintest.FramesOptions) {
	factory := func(context.Context) (plugin.Backend, error) { return process(path) }
	t.Run("Go session", func(t *testing.T) { plugintest.RunFrames(t, factory, opts) })
	t.Run("C engine, Go client", func(t *testing.T) {
		plugintest.RunFramesWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.FramesHost, error) {
			return goengine.Open(ctx, d, o)
		}, opts)
	})
	t.Run("C engine, engine-owned process", func(t *testing.T) {
		plugintest.RunFramesWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.FramesHost, error) {
			return goengine.OpenProcess(ctx, d, goengine.LaunchOptions{ProcessOptions: goengine.ProcessOptions{Executable: path}}, o)
		}, opts)
	})
}

func runGuestConformance(t *testing.T, factory plugintest.Factory) {
	plugintest.RunWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (plugintest.Host, error) {
		return goengine.Open(ctx, d, o)
	})
}
