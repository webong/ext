package plugintest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
)

// The Ruby guest SDK (pkg/ext-ruby) must pass the same suite through the
// process backend. EXT_RUBY names the interpreter (default: ruby on PATH);
// skipped when there is none, or it is older than Ruby 3.1.
func TestRubyGuest(t *testing.T) {
	ruby, dir := rubyFor(t)
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: ruby, Args: []string{"--disable-gems", filepath.Join(dir, "test", "conformance_guest.rb")}, Dir: dir})
	})
}

func TestRubyUnitTests(t *testing.T) {
	ruby, dir := rubyFor(t)
	if exec.Command(ruby, "-e", `require "minitest"`).Run() != nil {
		t.Skip("minitest is not installed for " + ruby)
	}
	cmd := exec.Command(ruby, filepath.Join("test", "all.rb"))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// rubyFor finds the interpreter and warms it up once with a generous bound, as
// warmUpPython does: the suite gives each handshake only a few seconds.
func rubyFor(t *testing.T) (string, string) {
	t.Helper()
	ruby := os.Getenv("EXT_RUBY")
	if ruby == "" {
		var err error
		if ruby, err = exec.LookPath("ruby"); err != nil {
			t.Skip("ruby not installed")
		}
	} else if !filepath.IsAbs(ruby) {
		t.Fatalf("EXT_RUBY must be an absolute path, got %q", ruby)
	}
	dir, err := filepath.Abs("../../../ext-ruby")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if exec.CommandContext(ctx, ruby, "-e", `exit(RUBY_VERSION >= "3.1" ? 0 : 1)`).Run() != nil {
		t.Skip(ruby + " is older than Ruby 3.1")
	}
	cmd := exec.CommandContext(ctx, ruby, "--disable-gems", "-I", filepath.Join(dir, "lib"), "-e", `require "ext_io/plugin"`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ruby warm-up failed: %v\n%s", err, out)
	}
	return ruby, dir
}
