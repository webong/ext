package plugintest_test

import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBindings(t *testing.T) {
	t.Run("inprocess", func(t *testing.T) {
		plugintest.Run(t, func(context.Context) (plugin.Backend, error) {
			g, err := plugintest.Guest()
			if err != nil {
				return nil, err
			}
			return inprocess.New(g)
		})
	})
	t.Run("jsonline", func(t *testing.T) {
		plugintest.Run(t, func(context.Context) (plugin.Backend, error) {
			g, err := plugintest.Guest()
			if err != nil {
				return nil, err
			}
			a, b := net.Pipe()
			go func() { _ = jsonline.ServeGuest(context.Background(), b, g) }()
			return jsonline.NewClient(a), nil
		})
	})
}
func TestFrozenV1Fixtures(t *testing.T) {
	data, err := os.ReadFile("../../../../lib/plugin/testdata/v1/descriptor.json")
	if err != nil {
		t.Fatal(err)
	}
	var d plugin.Descriptor
	if err := plugin.Decode(data, &d); err != nil {
		t.Fatal(err)
	}
	if err := plugin.MatchHandshake(d, plugintest.Descriptor()); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("../../../../lib/plugin/testdata/v1/invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	var invalid []string
	if err := json.Unmarshal(data, &invalid); err != nil {
		t.Fatal(err)
	}
	for _, input := range invalid {
		var raw json.RawMessage
		if plugin.Decode([]byte(input), &raw) == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}

// The Python guest SDK (pkg/ext-python) must pass the same suite through
// the process backend. Skipped when no python3 is installed.
func TestPythonGuest(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir, err := filepath.Abs("../../../ext-python")
	if err != nil {
		t.Fatal(err)
	}
	warmUpPython(t, python, dir)
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: python, Args: []string{"-I", filepath.Join(dir, "tests", "conformance_guest.py")}, Dir: dir})
	})
}

func TestPythonUnitTests(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir, err := filepath.Abs("../../../ext-python")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", "-m", "unittest", "discover", "-s", "tests")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// warmUpPython runs the guest's imports once with a generous bound. The
// conformance suite gives each handshake only a few seconds, and a cold first
// start of the interpreter (for example on a Windows runner with antivirus
// scanning) can exceed that, which would fail the first case for reasons that
// have nothing to do with the protocol.
func warmUpPython(t *testing.T, python, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "-c", "import sys; sys.path.insert(0, sys.argv[1]); import ext_plugin", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python warm-up failed: %v\n%s", err, out)
	}
}
