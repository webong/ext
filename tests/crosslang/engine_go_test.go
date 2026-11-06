//go:build !ext_cengine

package crosslang_test

import (
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
	"testing"
)

func runGuestConformance(t *testing.T, factory plugintest.Factory) { plugintest.Run(t, factory) }

// runFrames runs the frames suite on a guest through the pure-Go session. path names
// the guest when it is a process the C engine could own; it is not used here.
func runFrames(t *testing.T, factory plugintest.Factory, path string, opts plugintest.FramesOptions) {
	plugintest.RunFrames(t, factory, opts)
}
