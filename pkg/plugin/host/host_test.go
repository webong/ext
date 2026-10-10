package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/host"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/packagekit"
	"github.com/webong/ext/pkg/plugin/process"
	"github.com/webong/ext/pkg/plugin/store"
)

var contracts = []plugin.Contract{
	{ContractRef: plugin.ContractRef{Name: "example.content", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}, {Name: "fail"}, {Name: "model.inspect"}}},
	{ContractRef: plugin.ContractRef{Name: "other.provider", Version: "v1"}, Operations: []plugin.Operation{{Name: "observe"}}},
}

func descriptor() plugin.Descriptor {
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/host", Revision: "child", Version: "1.0.0"}, Contracts: contracts}
}

func TestMain(m *testing.M) {
	if os.Getenv("HOST_TEST_CHILD") == "1" {
		err := jsonline.ServeFunc(context.Background(), descriptor(), func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
			if method == "example.content.fail" {
				return nil, errors.New("child failure")
			}
			return map[string]any{"method": method, "payload": payload, "args": os.Args[1:]}, nil
		}, jsonline.FuncOptions{})
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// installExecutable builds a package whose executable is a copy of this test
// binary, installs it and returns it.
func installExecutable(t *testing.T) store.Installed {
	t.Helper()
	body, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	name := "provider"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(src, name), body, 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := packagekit.ExecutableManifest("example/host", "1.0.0", contracts, filepath.Join(src, name))
	if err != nil {
		t.Fatal(err)
	}
	m.Descriptor.Identity.Revision = "child" // the child serves a fixed identity
	if err := store.WriteManifest(src, m); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(filepath.Join(t.TempDir(), "plugins"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	return installed
}

func TestOpenAndCallExecutablePackage(t *testing.T) {
	installed := installExecutable(t)
	if !installed.IsExecutable() || installed.Name() != "example/host" || installed.Version() != "1.0.0" || !installed.Supports("other.provider", "v1") || installed.Supports("other.provider", "v2") {
		t.Fatal("installed helpers")
	}
	if path, err := installed.Executable(); err != nil || filepath.Dir(path) != installed.Directory {
		t.Fatalf("executable: %q %v", path, err)
	}
	p, err := host.Open(context.Background(), installed, host.Options{Launch: &process.Command{Path: mustExecutable(t, installed), Env: []string{"HOST_TEST_CHILD=1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out struct {
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.Call(ctx, "example.content.model.inspect", map[string]int{"n": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Method != "example.content.model.inspect" || string(out.Payload) != `{"n":1}` {
		t.Fatalf("%+v", out)
	}
	if err := p.Call(ctx, "other.observe", nil, &out); err != nil || out.Method != "other.provider.observe" || string(out.Payload) != "null" {
		t.Fatalf("short-prefix call: %+v %v", out, err)
	}
	var remote *plugin.RemoteError
	if err := p.Call(ctx, "example.content.fail", nil, nil); !errors.As(err, &remote) || remote.Message != "child failure" {
		t.Fatalf("remote error: %v", err)
	}
	if err := p.Call(ctx, "missing.method", nil, nil); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("unresolved method: %v", err)
	}
	if err := p.Call(ctx, "example.content.echo", func() {}, nil); err == nil {
		t.Fatal("unencodable params accepted")
	}
}

func TestLaunchOverrideAndAuthorize(t *testing.T) {
	installed := installExecutable(t)
	exe := mustExecutable(t, installed)
	var denied []string
	p, err := host.Open(context.Background(), installed, host.Options{
		Launch: &process.Command{Path: exe, Args: []string{"-test.run=^$", "script.py"}, Env: []string{"HOST_TEST_CHILD=1"}},
		Authorize: func(_ context.Context, r plugin.Request) error {
			if r.Operation == "fail" {
				denied = append(denied, r.Operation)
				return plugin.ErrDenied
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out struct {
		Args []string `json:"args"`
	}
	if err := p.Call(context.Background(), "example.content.echo", nil, &out); err != nil || len(out.Args) != 2 || out.Args[1] != "script.py" {
		t.Fatalf("launch arguments: %+v %v", out, err)
	}
	if err := p.Call(context.Background(), "example.content.fail", nil, nil); !errors.Is(err, plugin.ErrDenied) || len(denied) != 1 {
		t.Fatalf("authorize: %v %v", err, denied)
	}
}

func TestOpenRejections(t *testing.T) {
	installed := installExecutable(t)
	exe := mustExecutable(t, installed)
	if _, err := host.Open(context.Background(), installed, host.Options{Launch: &process.Command{}}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("empty launch path: %v", err)
	}
	// A package whose content changed after install never starts.
	if err := os.WriteFile(exe, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Open(context.Background(), installed, host.Options{}); err == nil || !strings.Contains(err.Error(), "denied") && !errors.Is(err, plugin.ErrDenied) && !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("tampered package: %v", err)
	}
	// A source-only package has no executable to start.
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "main.py"), []byte("x"), 0o600)
	m, err := packagekit.SourceManifest("example/src", "", contracts, src, []string{"main.py"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(src, m); err != nil {
		t.Fatal(err)
	}
	s, _ := store.New(filepath.Join(t.TempDir(), "p"), store.Limits{})
	source, err := s.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if source.IsExecutable() {
		t.Fatal("source package reports an executable")
	}
	if _, err := source.Executable(); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("source package executable: %v", err)
	}
	if _, err := host.Open(context.Background(), source, host.Options{}); err == nil {
		t.Fatal("started a package with no entrypoint and no launch override")
	}
}

func mustExecutable(t *testing.T, installed store.Installed) string {
	t.Helper()
	path, err := installed.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
