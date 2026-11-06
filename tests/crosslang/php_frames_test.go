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

// TestPHPFramesGuest runs the frames suite on the PHP guest SDK (pkg/ext-php). It reads
// inline, like the Python guest, so it is not concurrent. EXT_PHP_FRAMES_GUEST names a
// launcher; without one, the test writes a launcher for the php on PATH, and skips when
// there is none (or fails when EXT_FRAMES_REQUIRED lists PHP).
func TestPHPFramesGuest(t *testing.T) {
	path := os.Getenv("EXT_PHP_FRAMES_GUEST")
	if path == "" {
		path = phpFramesLauncher(t)
	}
	runFrames(t, func(context.Context) (plugin.Backend, error) { return process(path) }, path, plugintest.FramesOptions{Concurrent: false})
}

// phpFramesLauncher writes a script that starts the frames guest with the php on PATH,
// named by absolute path, since the engine starts a guest with an empty environment.
func phpFramesLauncher(t *testing.T) string {
	t.Helper()
	php, err := exec.LookPath("php")
	if err == nil {
		php, err = filepath.Abs(php)
	}
	if err != nil || runtime.GOOS == "windows" {
		if phpFramesRequired() {
			t.Fatal("EXT_FRAMES_REQUIRED lists PHP, but no php launcher is available")
		}
		t.Skip("set EXT_PHP_FRAMES_GUEST or install php")
	}
	guest, err := filepath.Abs("../../pkg/ext-php/tests/frames_guest.php")
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(t.TempDir(), "ext-php-frames-guest")
	script := "#!/bin/sh\nexec '" + php + "' '" + guest + "'\n"
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func phpFramesRequired() bool {
	for _, r := range strings.FieldsFunc(os.Getenv("EXT_FRAMES_REQUIRED"), func(c rune) bool { return c == ' ' || c == ',' }) {
		if r == "PHP" {
			return true
		}
	}
	return false
}
