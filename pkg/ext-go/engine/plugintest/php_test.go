package plugintest_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
)

// The PHP guest SDK (pkg/ext-php) must pass the same suite through the process
// backend. Skipped when no php is installed.
func TestPHPGuest(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	dir, err := filepath.Abs("../../../ext-php")
	if err != nil {
		t.Fatal(err)
	}
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: php, Args: []string{filepath.Join(dir, "tests", "conformance_guest.php")}, Dir: dir})
	})
}

func TestPHPUnitTests(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	dir, err := filepath.Abs("../../../ext-php")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, php, filepath.Join("tests", "run.php"))
	cmd.Dir = dir
	// On Windows a guest that run.php starts inherits run.php's output handles, so the pipe can stay
	// open after run.php has exited; stop reading a few seconds after it ends instead of waiting for it.
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	if errors.Is(err, exec.ErrWaitDelay) {
		t.Logf("a child of run.php still held its output open when run.php ended\n%s", out)
		return
	}
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
