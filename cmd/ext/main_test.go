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
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/engine/package"
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
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	bin := t.TempDir()
	ext := filepath.Join(bin, "ext"+exe)
	goBuild(t, ext, ".")

	pkg := t.TempDir()
	guest := filepath.Join(pkg, "hello"+exe)
	goBuild(t, guest, "./testdata/guest")
	m, err := packagekit.ExecutableManifest("example/hello", "1.0.0", []plugin.Contract{pkgcmd.RunContract}, guest)
	if err != nil {
		t.Fatal(err)
	}
	m.Descriptor.Identity.Revision = "child" // the guest serves a fixed identity
	m.Metadata = map[string]json.RawMessage{pkgcmd.MetadataKey: json.RawMessage(`{"commands":[{"name":"hi"},{"name":"bye"}]}`)}
	if err := store.WriteManifest(pkg, m); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	run := func(args ...string) (int, string, string) {
		cmd := exec.Command(ext, args...)
		cmd.Env = append(os.Environ(), "EXT_HOME="+home)
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

	if code, out, errOut := run("package", "install", pkg); code != 0 || !strings.Contains(out, "ext package trust example/hello") {
		t.Fatalf("install = %d %q %q", code, out, errOut)
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
	if _, out, _ := run("commands"); !strings.Contains(out, "ctx") || !strings.Contains(out, "hi") {
		t.Fatalf("commands = %q", out)
	}
	if code, _, _ := run("package", "remove", "example/hello"); code != 0 {
		t.Fatal("remove failed")
	}
	if code, _, _ := run("hi"); code != 2 {
		t.Fatal("hi still runs after remove")
	}
}
