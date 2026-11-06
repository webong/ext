package crosslang_test

import (
	"context"
	"os"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
)

// TestLuaFramesGuest runs the frames suite (progress events and reverse calls) on the Lua
// guest SDK (pkg/ext-lua/tests/frames_guest.lua). EXT_LUA_FRAMES_GUEST names a launcher
// for it, which scripts/ext-lua.sh writes. The guest reads inline, so it is not
// concurrent: it answers the operations that need a reader of its own with "unsupported".
func TestLuaFramesGuest(t *testing.T) {
	if os.Getenv("EXT_LUA_FRAMES_GUEST") == "" {
		t.Skip("set EXT_LUA_FRAMES_GUEST to a launcher of pkg/ext-lua/tests/frames_guest.lua, or run scripts/ext-lua.sh")
	}
	path := artifact(t, "LUA_FRAMES_GUEST")
	runFrames(t, func(context.Context) (plugin.Backend, error) { return process(path) }, path, plugintest.FramesOptions{Concurrent: false})
}
