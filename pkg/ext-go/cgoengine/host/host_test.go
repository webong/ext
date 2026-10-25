package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cgoengine/host"
	"github.com/webong/ext/pkg/ext-go/cgoengine/host/store"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/package"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
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
		if os.Getenv("HOST_TEST_NOISE") == "1" {
			fmt.Fprint(os.Stderr, strings.Repeat("e", 10000))
		}
		err := jsonline.ServeFunc(context.Background(), descriptor(), func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
			if method == "example.content.fail" {
				return nil, errors.New("child failure")
			}
			cwd, _ := os.Getwd()
			return map[string]any{"method": method, "payload": payload, "args": os.Args[1:], "cwd": cwd, "env": os.Environ()}, nil
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

type diag struct {
	Cwd string   `json:"cwd"`
	Env []string `json:"env"`
}

func envKeys(env []string) []string {
	var keys []string
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The C engine's launch must match pkg/ext-go/engine/host/process: working directory
// defaulting to the package directory, a scrubbed environment of PATH plus the
// caller's entries, and bounded retained stderr.
func TestLaunchMatchesGoProcess(t *testing.T) {
	installed := installExecutable(t)
	exe := mustExecutable(t, installed)
	env := []string{"HOST_TEST_CHILD=1", "HOST_TEST_NOISE=1", "EXTRA=1"}

	p, err := host.Open(context.Background(), installed, host.Options{Launch: &process.Command{Path: exe, Env: env, StderrBytes: 100}})
	if err != nil {
		t.Fatal(err)
	}
	var got diag
	if err := p.Call(context.Background(), "example.content.echo", nil, &got); err != nil {
		t.Fatal(err)
	}
	stderr := settledStderr(p.Stderr, 100)
	p.Close()

	// The reference: the same installed package launched by the pure-Go process
	// runtime, composed from contract.Open and process.Start.
	var want diag
	goStderr := goReference(t, installed, exe, env, 100, &want)

	resolve := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }
	if resolve(got.Cwd) != resolve(installed.Directory) {
		t.Errorf("C working directory %q, want the package directory %q", got.Cwd, installed.Directory)
	}
	if resolve(want.Cwd) != resolve(installed.Directory) {
		t.Errorf("Go working directory %q, want %q", want.Cwd, installed.Directory)
	}
	ck, gk := envKeys(got.Env), envKeys(want.Env)
	if strings.Join(ck, ",") != strings.Join(gk, ",") {
		t.Errorf("environment keys: C %v, Go %v", ck, gk)
	}
	for _, k := range []string{"PATH", "HOST_TEST_CHILD", "HOST_TEST_NOISE", "EXTRA"} {
		found := false
		for _, e := range got.Env {
			found = found || strings.HasPrefix(e, k+"=")
		}
		if !found {
			t.Errorf("C child lacks %s: %v", k, got.Env)
		}
	}
	for _, e := range got.Env {
		if strings.HasPrefix(e, "HOME=") {
			t.Error("host environment leaked into the child")
		}
	}
	if stderr != goStderr || len(stderr) != 100 || strings.Trim(stderr, "e") != "" {
		t.Errorf("retained stderr: C %d bytes, Go %d bytes (both must be the first 100)", len(stderr), len(goStderr))
	}
}

func TestLaunchPathEnvironmentOverrideAndRejections(t *testing.T) {
	installed := installExecutable(t)
	exe := mustExecutable(t, installed)
	// Keep the host's directories on PATH so a binary that loads DLLs from them
	// still starts; the point is that the caller's value replaces the base.
	custom := "custom" + string(os.PathListSeparator) + os.Getenv("PATH")
	p, err := host.Open(context.Background(), installed, host.Options{Launch: &process.Command{Path: exe, Env: []string{"HOST_TEST_CHILD=1", "PATH=" + custom}}})
	if err != nil {
		t.Fatal(err)
	}
	var got diag
	if err := p.Call(context.Background(), "example.content.echo", nil, &got); err != nil {
		t.Fatal(err)
	}
	p.Close()
	paths := 0
	for _, e := range got.Env {
		if strings.HasPrefix(e, "PATH=") {
			paths++
			if e != "PATH="+custom {
				t.Errorf("caller PATH not honored: %s", e)
			}
		}
	}
	if paths != 1 {
		t.Errorf("want one PATH entry, got %d", paths)
	}
	for _, bad := range []process.Command{{Path: filepath.Join(installed.Directory, "missing")}, {Path: installed.Directory}} {
		bad := bad
		if _, err := host.Open(context.Background(), installed, host.Options{Launch: &bad}); err == nil {
			t.Errorf("launch %q accepted", bad.Path)
		}
	}
	if _, err := host.Open(context.Background(), installed, host.Options{Launch: &process.Command{Path: exe, Env: []string{"NOEQUALS"}}}); err == nil {
		t.Error("malformed environment entry accepted")
	}
}

// goReference launches the installed package with the pure-Go process runtime,
// calls echo, fills diagnostics, and returns the retained stderr.
func goReference(t *testing.T, installed store.Installed, exe string, env []string, stderrBytes int, out *diag) string {
	t.Helper()
	var proc *process.Process
	session, err := plugin.Open(context.Background(), installed.Manifest.Descriptor, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return installed.Verify() },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Timeout:   host.DefaultTimeout,
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			p, err := process.Start(ctx, process.Command{Path: exe, Dir: installed.Directory, Env: env, StderrBytes: stderrBytes})
			proc = p
			return p, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	ref, operation, err := session.Descriptor().ResolveMethod("example.content.echo")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := session.Call(context.Background(), ref, operation, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
	return settledStderr(proc.Stderr, stderrBytes)
}

// settledStderr polls read until it holds want bytes. The child's stderr is
// copied by a goroutine of the parent, so what the child wrote before its reply
// can still be on its way when the reply has already been read.
func settledStderr(read func() string, want int) string {
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := read()
		if len(s) >= want || time.Now().After(deadline) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Options.Env alone starts the default launch of an installed package, and a
// child sees the entries added to its scrubbed environment.
func TestOptionsEnvReachesTheChildOfTheDefaultLaunch(t *testing.T) {
	installed := installExecutable(t)
	p, err := host.Open(context.Background(), installed, host.Options{Env: []string{"HOST_TEST_CHILD=1", "WORKER_PATH=/opt/worker"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out struct {
		Env []string `json:"env"`
	}
	if err := p.Call(context.Background(), "example.content.echo", nil, &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range out.Env {
		if entry == "WORKER_PATH=/opt/worker" {
			found = true
		}
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "USER=") {
			t.Fatalf("the child inherited %q: the environment must stay scrubbed", entry)
		}
	}
	if !found {
		t.Fatalf("Options.Env did not reach the child: %v", out.Env)
	}
}

// With a Launch the entries are added to Launch.Env; a repeated name is refused
// rather than resolved by order.
func TestOptionsEnvAddsToLaunchEnvAndRefusesDuplicates(t *testing.T) {
	installed := installExecutable(t)
	exe := mustExecutable(t, installed)
	p, err := host.Open(context.Background(), installed, host.Options{
		Launch: &process.Command{Path: exe, Env: []string{"HOST_TEST_CHILD=1"}},
		Env:    []string{"EXTRA=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Env []string `json:"env"`
	}
	if err := p.Call(context.Background(), "example.content.echo", nil, &out); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	have := map[string]bool{}
	for _, entry := range out.Env {
		have[entry] = true
	}
	if !have["EXTRA=1"] || !have["HOST_TEST_CHILD=1"] {
		t.Fatalf("both Launch.Env and Options.Env must reach the child: %v", out.Env)
	}
	for name, options := range map[string]host.Options{
		"repeated across the lists": {Launch: &process.Command{Path: exe, Env: []string{"A=1"}}, Env: []string{"A=2"}},
		"repeated within Env":       {Env: []string{"A=1", "A=2"}},
		"no equals sign":            {Env: []string{"NOEQUALS"}},
		"empty name":                {Env: []string{"=x"}},
		"NUL byte":                  {Env: []string{"A=1\x00"}},
	} {
		if _, err := host.Open(context.Background(), installed, options); !errors.Is(err, plugin.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Done lets a host tie anything to the plugin's lifetime without reaching into
// the session.
func TestProcessDoneClosesWithTheProcess(t *testing.T) {
	installed := installExecutable(t)
	p, err := host.Open(context.Background(), installed, host.Options{Env: []string{"HOST_TEST_CHILD=1"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
		t.Fatal("Done closed while the process was open")
	default:
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done did not close after Close")
	}
}

// Open's context bounds the start and the handshake only: cancelling it
// afterwards neither stops the process nor breaks later calls.
func TestOpenContextOnlyBoundsStartup(t *testing.T) {
	installed := installExecutable(t)
	ctx, cancel := context.WithCancel(context.Background())
	p, err := host.Open(ctx, installed, host.Options{Env: []string{"HOST_TEST_CHILD=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cancel()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-p.Done():
		t.Fatal("cancelling Open's context ended the process")
	default:
	}
	var out struct {
		Method string `json:"method"`
	}
	if err := p.Call(context.Background(), "example.content.echo", nil, &out); err != nil || out.Method != "example.content.echo" {
		t.Fatalf("call after cancelling Open's context: %+v %v", out, err)
	}
}
