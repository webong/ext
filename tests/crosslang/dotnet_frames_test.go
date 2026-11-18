package crosslang_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
)

// TestDotnetFramesGuest runs the frames suite on the .NET guest SDK (pkg/ext-dotnet/ExtIO).
// It reads inline, like the Python, PHP and Ruby guests, so it is not concurrent.
// EXT_DOTNET_FRAMES_GUEST names a launcher; without one, the test writes a launcher for the
// built ExtIO.Conformance.dll that EXT_DOTNET_GUEST names, run by EXT_DOTNET or the dotnet on
// PATH, and skips when there is none (or fails when EXT_FRAMES_REQUIRED lists Dotnet).
func TestDotnetFramesGuest(t *testing.T) {
	path := os.Getenv("EXT_DOTNET_FRAMES_GUEST")
	if path == "" {
		path = dotnetFramesLauncher(t)
	}
	runFrames(t, func(context.Context) (plugin.Backend, error) { return process(path) }, path, plugintest.FramesOptions{Concurrent: false})
}

// dotnetFramesLauncher writes a script that starts the frames guest with the chosen dotnet,
// named by absolute path, since the engine starts a guest with an empty environment.
func dotnetFramesLauncher(t *testing.T) string {
	t.Helper()
	guest := os.Getenv("EXT_DOTNET_GUEST")
	dotnet := os.Getenv("EXT_DOTNET")
	var err error
	if dotnet == "" {
		dotnet, err = exec.LookPath("dotnet")
	}
	if err == nil {
		dotnet, err = filepath.Abs(dotnet)
	}
	if guest == "" || err != nil || runtime.GOOS == "windows" {
		if dotnetFramesRequired() {
			t.Fatal("EXT_FRAMES_REQUIRED lists Dotnet, but no dotnet launcher is available")
		}
		t.Skip("set EXT_DOTNET_FRAMES_GUEST, or EXT_DOTNET_GUEST to a built ExtIO.Conformance.dll")
	}
	descriptor, err := filepath.Abs("../../lib/plugin/testdata/v1/frames-descriptor.json")
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(t.TempDir(), "ext-dotnet-frames-guest")
	script := "#!/bin/sh\nexec '" + dotnet + "' '" + guest + "' frames '" + descriptor + "'\n"
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func dotnetFramesRequired() bool {
	for _, r := range strings.FieldsFunc(os.Getenv("EXT_FRAMES_REQUIRED"), func(c rune) bool { return c == ' ' || c == ',' }) {
		if r == "Dotnet" {
			return true
		}
	}
	return false
}
