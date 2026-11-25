package pkgcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/cmd/ext/internal/registry"
	"github.com/webong/ext/pkg/ext-go/cengine/plugin/host"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
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
	if os.Getenv("PKGCMD_CHILD") == "1" || os.Getenv("EXT_COMMAND_ADDR") != "" {
		fn := func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
			var req request
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, err
			}
			report := func(stream, text string) {
				_ = plugin.ProgressFrom(ctx).Report(plugin.Progress{Kind: "log", Stream: stream, Text: text})
			}
			switch req.Command {

			case "snapshot":
				if len(req.Args) != 1 {
					return nil, fmt.Errorf("missing home")
				}
				lock := filepath.Join(req.Args[0], ".package-write.lock")
				if err := os.Mkdir(lock, 0700); err != nil {
					return nil, err
				}
				if err := os.Remove(lock); err != nil {
					return nil, err
				}
				executable, err := os.Executable()
				if err != nil {
					return nil, err
				}
				if os.Getenv("EXT_COMMAND_ADDR") != "" {
					fmt.Fprintln(os.Stdout, executable)
				} else {
					report("out", executable+"\n")
				}
				return response{}, nil
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
			case "pipe":
				for i := 0; i < 1000; i++ {
					fmt.Fprintln(os.Stdout, "line")
				}
				return response{}, nil
			}
			return nil, fmt.Errorf("unknown command %q", req.Command)
		}
		var err error
		if address := os.Getenv("EXT_COMMAND_ADDR"); address != "" {
			var conn net.Conn
			conn, err = net.Dial("tcp", address)
			if err == nil {
				_, err = io.WriteString(conn, os.Getenv("EXT_COMMAND_TOKEN")+"\n")
			}
			if err == nil {
				var guest *plugin.Guest
				guest, err = jsonline.NewFuncGuest(descriptor(), fn, jsonline.FuncOptions{})
				if err == nil {
					err = jsonline.ServeGuest(context.Background(), conn, guest)
				}
			}
		} else {
			err = jsonline.ServeFunc(context.Background(), descriptor(), fn, jsonline.FuncOptions{})
		}
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
	s, err := store.New(filepath.Join(home, "packages"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	Discover(reg, s, trust, childEnv)
	return reg
}

func TestDiscoverNeedsTrustAndTrustFollowsTheContent(t *testing.T) {
	home := t.TempDir()
	installed := install(t, filepath.Join(home, "packages"), "1.0.0", declares)

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

	install(t, filepath.Join(home, "packages"), "1.1.0", declares) // new content
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
	installed := install(t, filepath.Join(home, "packages"), "1.0.0", declares)
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

func TestSocketTransportRunsInstalledPackage(t *testing.T) {
	home := t.TempDir()
	installed := install(t, filepath.Join(home, "packages"), "1.0.0", `{"commands":[{"name":"echo","transport":"socket"}]}`)
	trust, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := trust.Trust(installed); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCommand(t, discover(t, home), "echo", "socket")
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("socket = %d %q %q", code, out, errOut)
	}
}

func TestSocketTransportTreatsClosedPipelineAsSIGPIPE(t *testing.T) {
	home := t.TempDir()
	installed := install(t, filepath.Join(home, "packages"), "1.0.0", `{"commands":[{"name":"pipe","transport":"socket"}]}`)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	defer writer.Close()
	var stderr bytes.Buffer
	code := runSocket(installed, Declared{Name: "pipe", Transport: "socket"}, nil, writer, &stderr)
	if code != 141 || stderr.Len() != 0 {
		t.Fatalf("closed pipeline = exit %d, stderr %q", code, stderr.String())
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
	s, err := store.New(filepath.Join(home, "packages"), store.Limits{})
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
	if code, _, errOut := manage(t, m, "trust", "example/tools"); code != 0 {
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

// A provider is installable and trustable without implementing ext.command/v1.
// Approval remains tied to its contents and does not manufacture a CLI command.
func TestManageProviderLifecycle(t *testing.T) {
	m, home := manager(t)
	src := source(t, "1.0.0", "")
	manifestPath := filepath.Join(src, store.ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest packagekit.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Descriptor.Contracts = []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.provider", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}
	if err := store.WriteManifest(src, manifest); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"install", src}, {"trust", "example/tools"}} {
		if code, out, stderr := manage(t, m, args...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
	}
	installed, err := m.Store.Load("example/tools")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := reloaded.Trusted(installed); err != nil || !ok {
		t.Fatalf("persisted trust: %v %v", ok, err)
	}
	if _, _, err := discover(t, home).Resolve("read", ""); err == nil {
		t.Fatal("provider operation became a command")
	}
	if code, out, stderr := manage(t, m, "list"); code != 0 || !strings.Contains(out, "trusted") || strings.Contains(out, "untrusted") {
		t.Fatalf("list: %d %s %s", code, out, stderr)
	}
	if code, _, stderr := manage(t, m, "untrust", "example/tools"); code != 0 {
		t.Fatal(stderr)
	}
	if ok, err := m.Trust.Trusted(installed); err != nil || ok {
		t.Fatalf("revoked trust: %v %v", ok, err)
	}
	if code, _, stderr := manage(t, m, "trust", "example/tools"); code != 0 {
		t.Fatal(stderr)
	}
	artifact, err := installed.ArtifactPath(manifest.Artifacts[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := manage(t, m, "trust", "example/tools"); code == 0 {
		t.Fatal("trusted tampered provider")
	}
	if code, _, stderr := manage(t, m, "remove", "example/tools"); code != 0 {
		t.Fatal(stderr)
	}
	reloaded, err = LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Packages) != 0 {
		t.Fatal("remove retained trust")
	}
}

func TestManageInvalidCommandsDoNotBlockPackageTrust(t *testing.T) {
	m, home := manager(t)
	src := source(t, "1.0.0", `{"commands":[{"name":"bad name"}]}`)
	if code, out, stderr := manage(t, m, "install", src); code != 0 || !strings.Contains(out, "commands unavailable") {
		t.Fatalf("install: %d %s %s", code, out, stderr)
	}
	if code, out, stderr := manage(t, m, "trust", "example/tools"); code != 0 {
		t.Fatalf("trust: %d %s %s", code, out, stderr)
	}
	reg := discover(t, home)
	if _, _, err := reg.Resolve("bad name", ""); err == nil {
		t.Fatal("invalid command registered")
	}
	if len(reg.Notes()) == 0 {
		t.Fatal("invalid command declaration not diagnosed")
	}
}

func TestSocketCommandChecksAdmissionBeforeLaunch(t *testing.T) {
	var stderr bytes.Buffer
	called := false
	command := Command(store.Installed{}, Declared{Name: "blocked", Transport: "socket"}, host.Options{Verify: func(context.Context) error { called = true; return errors.New("dependency approval revoked") }})
	if got := command.Run(nil, io.Discard, &stderr); got != 1 || !called || !strings.Contains(stderr.String(), "dependency approval revoked") {
		t.Fatalf("admission: %d %t %s", got, called, stderr.String())
	}
}

func TestManagementRefusesConcurrentWriter(t *testing.T) {
	m, home := manager(t)
	lock := filepath.Join(home, ".package-write.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lock)
	for _, command := range []string{"install", "upgrade", "remove", "trust", "untrust", "add", "refresh"} {
		code, _, stderr := manage(t, m, command, "unused")
		if code != 1 || !strings.Contains(stderr, "package writer unavailable") {
			t.Fatalf("%s bypassed writer lock: %d %s", command, code, stderr)
		}
	}
}

func TestDiscoveredCommandsRunFromPrivateSnapshots(t *testing.T) {
	for _, transport := range []string{"", "socket"} {
		name := transport
		if name == "" {
			name = "stdio"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			metadata := fmt.Sprintf(`{"commands":[{"name":"snapshot","transport":%q}]}`, transport)
			installed := install(t, filepath.Join(home, "packages"), "1.0.0", metadata)
			trust, err := LoadTrust(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := trust.Trust(installed); err != nil {
				t.Fatal(err)
			}
			reg := discover(t, home)
			code, out, diagnostics := runCommand(t, reg, "snapshot", home)
			if code != 0 {
				t.Fatalf("snapshot command: %d %s", code, diagnostics)
			}
			path := strings.TrimSpace(out)
			if path == "" || strings.HasPrefix(path, installed.Directory+string(os.PathSeparator)) {
				t.Fatalf("executed original package: %q", path)
			}
			if !strings.Contains(path, "ext-command-snapshot-") {
				t.Fatalf("not private snapshot: %q", path)
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("snapshot not cleaned: %v", err)
			}
			// The registry must not retain admission after trust is revoked.
			if err := trust.Revoke(installed.Name()); err != nil {
				t.Fatal(err)
			}
			if code, _, _ := runCommand(t, reg, "snapshot", home); code == 0 {
				t.Fatal("cached command bypassed revoked trust")
			}
		})
	}
}

func TestCommandSnapshotSurvivesSourceRemoval(t *testing.T) {
	home := t.TempDir()
	installed := install(t, filepath.Join(home, "packages"), "1.0.0", declares)
	trust, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := trust.Trust(installed); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(filepath.Join(home, "packages"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	copied, cleanup, err := prepareCommandSnapshot(installed, childEnv, s, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.RemoveAll(installed.Directory); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := Command(copied, Declared{Name: "echo"}, childEnv).Run([]string{"copied"}, &out, &diagnostics); code != 0 || out.String() != "copied\n" {
		t.Fatalf("isolated invocation: %d %s %s", code, out.String(), diagnostics.String())
	}
}
