package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func goBuild(t *testing.T, out, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, b)
	}
}

// TestEndToEnd drives the real ext binary: install a package that provides commands,
// see that it is not run until trusted, trust it, run its commands, and remove it.
func TestEndToEnd(t *testing.T) {
	native := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if native == "" {
		t.Skip("run scripts/package-acceptance.sh for command snapshot acceptance")
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	bin := t.TempDir()
	ext := filepath.Join(bin, "ext"+exe)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, native)

	pkg := t.TempDir()
	guest := filepath.Join(pkg, "hello"+exe)
	goBuild(t, guest, "./testdata/guest")
	m, err := packagekit.ExecutableManifest("example/hello", "1.0.0", []plugin.Contract{pkgcmd.RunContract}, guest)
	if err != nil {
		t.Fatal(err)
	}
	m.Descriptor.Identity.Revision = "child" // the guest serves a fixed identity
	m.Metadata = map[string]json.RawMessage{pkgcmd.MetadataKey: json.RawMessage(`{"commands":[{"name":"hi"},{"name":"bye"}]}`)}
	manifestPath := "config/peer.json"
	if err := store.WriteManifestAt(pkg, manifestPath, m); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	outside := t.TempDir()
	run := func(args ...string) (int, string, string) {
		cmd := exec.Command(ext, args...)
		cmd.Dir = outside
		cmd.Env = acceptanceEnv(home)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = ee.ExitCode()
		}
		return code, out.String(), errOut.String()
	}

	if code, out, errOut := run("bundle", "help"); code != 0 || !strings.Contains(out, "ext bundle fetch") || !strings.Contains(out, "ext bundle bind") {
		t.Fatalf("bundle help = %d %q %q", code, out, errOut)
	}
	if code, _, errOut := run("lib", "help"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("removed lib command = %d %q", code, errOut)
	}
	if code, _, errOut := run("package", "install", pkg, "--manifest", "../peer.json"); code != 1 || !strings.Contains(errOut, "invalid package manifest layout") {
		t.Fatalf("outside manifest path = %d %q", code, errOut)
	}
	conflict := filepath.Join(pkg, storelayout.ExtManifest)
	if err := os.WriteFile(conflict, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run("package", "install", pkg, "--manifest", manifestPath); code != 1 || !strings.Contains(errOut, "managed manifest path") {
		t.Fatalf("manifest collision = %d %q", code, errOut)
	}
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := run("package", "install", pkg, "--manifest", manifestPath); code != 0 || !strings.Contains(out, "ext package trust example/hello") {
		t.Fatalf("install = %d %q %q", code, out, errOut)
	}
	for _, path := range []string{
		filepath.Join(pkg, filepath.FromSlash(manifestPath)),
		filepath.Join(home, "packages", "example%2fhello", storelayout.ExtManifest),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, errOut := run("hi", "x"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("untrusted hi = %d %q", code, errOut)
	}
	if _, _, errOut := run("commands"); !strings.Contains(errOut, "not trusted") {
		t.Fatalf("commands did not say why hi is missing: %q", errOut)
	}
	if code, _, errOut := run("package", "trust", "example/hello"); code != 0 {
		t.Fatalf("trust = %d %q", code, errOut)
	}
	if code, out, errOut := run("hi", "hello", "world"); code != 0 || out != "hello world\n" {
		t.Fatalf("hi = %d %q %q", code, out, errOut)
	}
	if code, out, errOut := run("bye"); code != 4 || out != "" || errOut != "bye\n" {
		t.Fatalf("bye = %d %q %q", code, out, errOut)
	}
	if _, out, _ := run("which", "hi"); !strings.Contains(out, "hi: package 1.0.0") {
		t.Fatalf("which = %q", out)
	}
	if _, out, _ := run("commands"); !strings.Contains(out, "bundle") || !strings.Contains(out, "hi") {
		t.Fatalf("commands = %q", out)
	}
	// A new sealed revision requires approval of the new digest.
	buildUpgrade := exec.Command("go", "build", "-ldflags=-X main.revision=child2", "-o", guest, "./testdata/guest")
	if output, err := buildUpgrade.CombinedOutput(); err != nil {
		t.Fatalf("build upgrade: %v\n%s", err, output)
	}
	m, err = packagekit.ExecutableManifest("example/hello", "1.0.0", []plugin.Contract{pkgcmd.RunContract}, guest)
	if err != nil {
		t.Fatal(err)
	}
	m.Descriptor.Identity.Revision = "child2"
	m.Metadata = make(map[string]json.RawMessage)
	m.Metadata[pkgcmd.MetadataKey] = json.RawMessage(`{"commands":[{"name":"hi","summary":"Updated greeting"},{"name":"bye"}]}`)
	if err := store.WriteManifestAt(pkg, manifestPath, m); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run("package", "upgrade", pkg, "--manifest", manifestPath); code != 0 {
		t.Fatalf("upgrade = %d %q", code, errOut)
	}
	if code, _, _ := run("hi", "unapproved-upgrade"); code != 2 {
		t.Fatal("upgraded package retained approval of different bytes")
	}
	if code, _, errOut := run("package", "trust", "example/hello"); code != 0 {
		t.Fatalf("approve upgrade = %d %q", code, errOut)
	}
	if code, _, _ := run("package", "remove", "example/hello"); code != 0 {
		t.Fatal("remove failed")
	}
	if code, _, _ := run("hi"); code != 2 {
		t.Fatal("hi still runs after remove")
	}
}

// acceptanceEnv prevents a developer's configured providers or Go workspace
// from participating in the runtime check. Only staged artifacts are available.
func acceptanceEnv(home string) []string {
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "EXT_") || key == "GOWORK" {
			continue
		}
		env = append(env, value)
	}
	return append(env, "EXT_HOME="+home, "GOWORK=off")
}

func stageAcceptanceEngine(t *testing.T, bin, source string) {
	t.Helper()
	name := "libext_plugin_host.so"
	ledgerName := "libext_ledger.so"
	if runtime.GOOS == "darwin" {
		name = "libext_plugin_host.dylib"
		ledgerName = "libext_ledger.dylib"
	}
	if runtime.GOOS == "windows" {
		name = "ext_plugin_host.dll"
		ledgerName = "ext_ledger.dll"
	}
	dir := filepath.Join(bin, "ext-lib")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{name: source, ledgerName: os.Getenv("EXT_LEDGER_LIBRARY")} {
		if source == "" {
			t.Fatal("acceptance requires EXT_LEDGER_LIBRARY alongside EXT_ACCEPTANCE_LIBRARY")
		}
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
}
