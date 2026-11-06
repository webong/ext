package plugintest_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

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
	cmd := exec.Command(php, filepath.Join("tests", "run.php"))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
