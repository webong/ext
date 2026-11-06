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

// TestRubyFramesGuest runs the frames suite on the Ruby guest SDK (pkg/ext-ruby). It reads
// inline, like the Python and PHP guests, so it is not concurrent. EXT_RUBY_FRAMES_GUEST
// names a launcher; without one, the test writes a launcher for EXT_RUBY or the ruby on
// PATH, and skips when there is none (or fails when EXT_FRAMES_REQUIRED lists Ruby).
func TestRubyFramesGuest(t *testing.T) {
	path := os.Getenv("EXT_RUBY_FRAMES_GUEST")
	if path == "" {
		path = rubyFramesLauncher(t)
	}
	runFrames(t, func(context.Context) (plugin.Backend, error) { return process(path) }, path, plugintest.FramesOptions{Concurrent: false})
}

// rubyFramesLauncher writes a script that starts the frames guest with the chosen ruby,
// named by absolute path, since the engine starts a guest with an empty environment.
func rubyFramesLauncher(t *testing.T) string {
	t.Helper()
	ruby := os.Getenv("EXT_RUBY")
	var err error
	if ruby == "" {
		ruby, err = exec.LookPath("ruby")
	}
	if err == nil {
		ruby, err = filepath.Abs(ruby)
	}
	if err != nil || runtime.GOOS == "windows" {
		if rubyFramesRequired() {
			t.Fatal("EXT_FRAMES_REQUIRED lists Ruby, but no ruby launcher is available")
		}
		t.Skip("set EXT_RUBY_FRAMES_GUEST or install ruby")
	}
	guest, err := filepath.Abs("../../pkg/ext-ruby/test/frames_guest.rb")
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(t.TempDir(), "ext-ruby-frames-guest")
	script := "#!/bin/sh\nexec '" + ruby + "' --disable-gems '" + guest + "'\n"
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func rubyFramesRequired() bool {
	for _, r := range strings.FieldsFunc(os.Getenv("EXT_FRAMES_REQUIRED"), func(c rune) bool { return c == ' ' || c == ',' }) {
		if r == "Ruby" {
			return true
		}
	}
	return false
}
