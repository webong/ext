package pkgcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/cmd/ext/internal/registry"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
)

func descriptor() plugin.Descriptor {
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/tools", Revision: "child", Version: "1.0.0"},
		Contracts:  []plugin.Contract{RunContract},
	}
}

// TestMain turns the test binary into the guest when PKGCMD_CHILD is set, the same
// way the host package's own tests do.
func TestMain(m *testing.M) {
	if os.Getenv("PKGCMD_CHILD") == "1" {
		err := jsonline.ServeFunc(context.Background(), descriptor(), func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
			var req request
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, err
			}
			report := func(stream, text string) {
				_ = plugin.ProgressFrom(ctx).Report(plugin.Progress{Kind: "log", Stream: stream, Text: text})
			}
			switch req.Command {
			case "echo":
				report("out", strings.Join(req.Args, " ")+"\n")
				return response{Exit: 0}, nil
			case "fail":
				report("err", "boom\n")
				return response{Exit: 3}, nil
			case "flood":
				for i := 0; i < 2000; i++ {
					report("out", fmt.Sprintf("line %d\n", i))
				}
				return response{}, nil
			case "where":
				report("out", req.Dir+"\n")
				return response{}, nil
			}
			return nil, fmt.Errorf("unknown command %q", req.Command)
		}, jsonline.FuncOptions{})
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var childEnv = host.Options{Env: []string{"PKGCMD_CHILD=1"}}

// source writes a package directory whose executable is a copy of this test binary;
// version changes the content.
func source(t *testing.T, version, metadata string) string {
	t.Helper()
	body, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	name := "tools"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(src, name), body, 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := packagekit.ExecutableManifest("example/tools", version, []plugin.Contract{RunContract}, filepath.Join(src, name))
	if err != nil {
		t.Fatal(err)
	}
	m.Descriptor.Identity.Revision = "child"
	if metadata != "" {
		m.Metadata = map[string]json.RawMessage{MetadataKey: json.RawMessage(metadata)}
	}
	if err := store.WriteManifest(src, m); err != nil {
		t.Fatal(err)
	}
	return src
}

// install replaces whatever is installed under root with a fresh package.
func install(t *testing.T, root, version, metadata string) store.Installed {
	t.Helper()
	s, err := store.New(root, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("example/tools"); err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	installed, err := s.Install(source(t, version, metadata))
	if err != nil {
		t.Fatal(err)
	}
	return installed
}

const declares = `{"commands":[{"name":"echo"},{"name":"fail"},{"name":"flood"},{"name":"where"}]}`

func TestDeclare(t *testing.T) {
	root := filepath.Join(t.TempDir(), "commands")
	if got, err := Declare(install(t, root, "1.0.0", declares)); err != nil || len(got) != 4 {
		t.Fatalf("declared = %v, %v", got, err)
	}
	if got, err := Declare(install(t, filepath.Join(t.TempDir(), "x"), "1.0.0", "")); err != nil || len(got) != 0 {
		t.Fatalf("no metadata = %v, %v", got, err)
	}
	for _, bad := range []string{`{"commands":[{"name":"Bad Name"}]}`, `{"commands":[{"name":"a"},{"name":"a"}]}`, `{"commands":"x"}`} {
		if _, err := Declare(install(t, filepath.Join(t.TempDir(), "y"), "1.0.0", bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func discover(t *testing.T, home string) *registry.Registry {
	t.Helper()
	trust, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.New(filepath.Join(home, "commands"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	Discover(reg, s, trust, childEnv)
	return reg
}

func TestDiscoverNeedsTrustAndTrustFollowsTheContent(t *testing.T) {
	home := t.TempDir()
	installed := install(t, filepath.Join(home, "commands"), "1.0.0", declares)

	reg := discover(t, home)
	if _, _, err := reg.Resolve("echo", ""); err == nil {
		t.Fatal("an untrusted package registered its commands")
	}
	if notes := reg.Notes(); len(notes) != 1 || !strings.Contains(notes[0], "not trusted") {
		t.Fatalf("notes = %v", notes)
	}

	trust, _ := LoadTrust(home)
	if err := trust.Trust(installed); err != nil {
		t.Fatal(err)
	}
	reg = discover(t, home)
	c, _, err := reg.Resolve("echo", "")
	if err != nil || c.Source != registry.Package || c.Version != "1.0.0" {
		t.Fatalf("trusted = %+v, %v", c, err)
	}

	install(t, filepath.Join(home, "commands"), "1.1.0", declares) // new content
	reg = discover(t, home)
	if _, _, err := reg.Resolve("echo", ""); err == nil {
		t.Fatal("a changed package kept its trust")
	}

	if err := trust.Revoke("example/tools"); err != nil {
		t.Fatal(err)
	}
	if again, _ := LoadTrust(home); len(again.Packages) != 0 {
		t.Fatalf("revoked record survives: %v", again.Packages)
	}
}

func trusted(t *testing.T) *registry.Registry {
	t.Helper()
	home := t.TempDir()
	installed := install(t, filepath.Join(home, "commands"), "1.0.0", declares)
	trust, _ := LoadTrust(home)
	if err := trust.Trust(installed); err != nil {
		t.Fatal(err)
	}
	return discover(t, home)
}

func runCommand(t *testing.T, reg *registry.Registry, name string, args ...string) (int, string, string) {
	t.Helper()
	c, _, err := reg.Resolve(name, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := c.Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunStreamsOutputAndReturnsTheExitCode(t *testing.T) {
	reg := trusted(t)
	if code, out, errOut := runCommand(t, reg, "echo", "a", "b"); code != 0 || out != "a b\n" || errOut != "" {
		t.Fatalf("echo = %d %q %q", code, out, errOut)
	}
	if code, out, errOut := runCommand(t, reg, "fail"); code != 3 || out != "" || errOut != "boom\n" {
		t.Fatalf("fail = %d %q %q", code, out, errOut)
	}
	cwd, _ := os.Getwd()
	if _, out, _ := runCommand(t, reg, "where"); strings.TrimSpace(out) != cwd {
		t.Fatalf("dir = %q, want %q", out, cwd)
	}
}

func TestRunKeepsEveryLineOfALargeOutput(t *testing.T) {
	_, out, errOut := runCommand(t, trusted(t), "flood")
	if got := strings.Count(out, "\n"); got != 2000 || errOut != "" {
		t.Fatalf("%d lines, stderr %q", got, errOut)
	}
	if !strings.HasPrefix(out, "line 0\nline 1\n") || !strings.HasSuffix(out, "line 1999\n") {
		t.Fatal("lines are out of order or truncated")
	}
}

func manager(t *testing.T) (Manager, string) {
	t.Helper()
	home := t.TempDir()
	s, err := store.New(filepath.Join(home, "commands"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	return Manager{Store: s, Trust: trust}, home
}

func manage(t *testing.T, m Manager, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := m.Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestManageInstallTrustListRemove(t *testing.T) {
	m, home := manager(t)
	src := source(t, "1.0.0", declares)

	code, out, errOut := manage(t, m, "install", src)
	if code != 0 || !strings.Contains(out, "not trusted") || !strings.Contains(out, "ext package trust example/tools") {
		t.Fatalf("install = %d %q %q", code, out, errOut)
	}
	if _, _, err := discover(t, home).Resolve("echo", ""); err == nil {
		t.Fatal("installing a package registered its commands")
	}
	if code, _, errOut := manage(t, m, "install", src); code != 1 || !strings.Contains(errOut, "already installed") {
		t.Fatalf("second install = %d %q", code, errOut)
	}
	if _, out, _ := manage(t, m, "list"); !strings.Contains(out, "untrusted") || !strings.Contains(out, "echo, fail, flood, where") {
		t.Fatalf("list = %q", out)
	}

	if code, out, errOut := manage(t, m, "trust", "example/tools"); code != 0 || !strings.Contains(out, "trusted example/tools 1.0.0") {
		t.Fatalf("trust = %d %q %q", code, out, errOut)
	}
	if c, _, err := discover(t, home).Resolve("echo", ""); err != nil || c.Source != registry.Package {
		t.Fatalf("after trust: %+v %v", c, err)
	}
	if _, out, _ := manage(t, m, "list"); !strings.Contains(out, "trusted") || strings.Contains(out, "untrusted") {
		t.Fatalf("list after trust = %q", out)
	}

	if code, _, _ := manage(t, m, "untrust", "example/tools"); code != 0 {
		t.Fatal("untrust failed")
	}
	if _, _, err := discover(t, home).Resolve("echo", ""); err == nil {
		t.Fatal("untrusted package still registered")
	}

	if code, _, _ := manage(t, m, "trust", "example/tools"); code != 0 {
		t.Fatal("trust failed")
	}
	if code, out, _ := manage(t, m, "remove", "example/tools"); code != 0 || !strings.Contains(out, "removed") {
		t.Fatalf("remove = %d %q", code, out)
	}
	if again, _ := LoadTrust(home); len(again.Packages) != 0 {
		t.Fatalf("remove left a trust record: %v", again.Packages)
	}
}

func TestManageRefusesWhatCannotBeTrustedOrUsed(t *testing.T) {
	m, _ := manager(t)
	if code, _, _ := manage(t, m, "install", source(t, "1.0.0", "")); code != 0 {
		t.Fatal("install without commands failed")
	}
	if code, _, errOut := manage(t, m, "trust", "example/tools"); code != 1 || !strings.Contains(errOut, "declares no commands") {
		t.Fatalf("trust without commands = %d %q", code, errOut)
	}
	if code, _, _ := manage(t, m, "trust", "no/such"); code != 1 {
		t.Fatal("trusted a package that is not installed")
	}
	for _, args := range [][]string{{}, {"bogus", "x"}, {"install"}, {"list", "extra"}} {
		if code, _, _ := manage(t, m, args...); code != 2 {
			t.Fatalf("%v exit %d, want 2", args, code)
		}
	}
}
