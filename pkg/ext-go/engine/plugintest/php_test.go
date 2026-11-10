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
	guest := filepath.Join(dir, "tests", "conformance_guest.php")
	warmUp(t, php, guest)
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: php, Args: []string{guest}, Dir: dir})
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
	warmUp(t, php, filepath.Join(dir, "tests", "conformance_guest.php"))
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

// warmUp runs the guest once with its input already at its end, so that the suite's first session
// does not pay for loading php, its extensions and the SDK from a cold disk. The suite gives a
// session three seconds, and a first start on a Windows runner has taken that long.
func warmUp(t *testing.T, php, guest string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, php, guest).Run(); err != nil {
		t.Logf("warm-up run of %s: %v", guest, err)
	}
}
