package plugintest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
)

// luaInterpreter is EXT_LUA, or the first Lua interpreter on the path. The Lua guest SDK
// (pkg/ext-lua) runs on Lua 5.1 to 5.5 and LuaJIT.
func luaInterpreter(t *testing.T) string {
	t.Helper()
	if lua := os.Getenv("EXT_LUA"); lua != "" {
		return lua
	}
	for _, name := range []string{"lua", "lua5.4", "lua5.5", "lua5.3", "luajit", "lua5.2", "lua5.1"} {
		if lua, err := exec.LookPath(name); err == nil {
			return lua
		}
	}
	t.Skip("no Lua interpreter installed (set EXT_LUA)")
	return ""
}

// The Lua guest SDK (pkg/ext-lua) must pass the same suite through the process
// backend. Skipped when no Lua interpreter is installed.
func TestLuaGuest(t *testing.T) {
	lua := luaInterpreter(t)
	dir, err := filepath.Abs("../../../ext-lua")
	if err != nil {
		t.Fatal(err)
	}
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: lua, Args: []string{filepath.Join(dir, "tests", "conformance_guest.lua")}, Dir: dir})
	})
}

func TestLuaUnitTests(t *testing.T) {
	lua := luaInterpreter(t)
	dir, err := filepath.Abs("../../../ext-lua")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(lua, filepath.Join("tests", "run.lua"))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
