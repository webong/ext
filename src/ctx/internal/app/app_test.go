package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/graph/system"
)

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), Version) {
		t.Fatalf("version output %q does not contain %q", stdout.String(), Version)
	}
}

func TestMissingRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"run"}, &stdout, &stderr); code != 2 {
		t.Fatalf("got exit code %d", code)
	}
}

func TestBrowserManagementCLIRequiresDeclaredOperation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	t.Setenv("CTX_ADAPTER_HOME", filepath.Join(root, "state", "adapters"))
	adapterDir := filepath.Join(root, "browser-adapter")
	if err := os.MkdirAll(adapterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version = "2.0"
name = "fixture"
runtime = "browser"
surfaces = "web"
executable = "ctx-fixture"
capabilities = "list,validate,open,doctor,share"
browser_management = "extension.prepare"
`
	if err := os.WriteFile(filepath.Join(adapterDir, "adapter.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell adapter fixture requires a Unix host")
	}
	executable := `#!/bin/sh
if [ "$#" -ne 6 ] || [ "$1" != share ] || [ "$2" != Default ] || [ "$3" != -- ] || [ "$4" != management ] || [ "$5" != extension ] || [ "$6" != prepare ]; then
    printf 'unexpected management invocation: %s\n' "$*" >&2
    exit 2
fi
cat >/dev/null
printf '%s\n' '{"version":"1.0","kind":"extension","action":"prepare","status":"prepared"}'
`
	if err := os.WriteFile(filepath.Join(adapterDir, "ctx-fixture"), []byte(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	store := adapterStore()
	installed, err := store.Install(adapterDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(installed); err != nil {
		t.Fatal(err)
	}
	run := func(action string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run([]string{"browser", "manage", "extension", action, "--target", "fixture:Default"}, &stdout, &stderr)
		return code, stderr.String()
	}
	if code, errText := run("prepare"); code != 0 {
		t.Fatalf("prepare exit=%d stderr=%q", code, errText)
	}
	if code, errText := run("install"); code == 0 || !strings.Contains(errText, "does not support management operation") {
		t.Fatalf("undeclared install exit=%d stderr=%q", code, errText)
	}
}

func TestCTXRecordsSystemContextAndGraphCommandReadsIt(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "ctx-state")
	t.Setenv("CTX_HOME", stateDir)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"resolve", "browser"}, &stdout, &stderr); code != 0 {
		t.Fatalf("resolve failed: %d %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"graph", "vertices", "shell-session"}, &stdout, &stderr); code != 0 {
		t.Fatalf("graph query failed: %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ctx.system/shell-session") {
		t.Fatalf("shell facts missing from graph query: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(stateDir, "graph.json")); err != nil {
		t.Fatalf("persistent CTX graph missing: %v", err)
	}
	store, err := systemgraph.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Store.Snapshot(context.Background(), systemgraph.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Vertices) < 4 {
		t.Fatalf("expected system facts, got %d vertices", len(snapshot.Vertices))
	}
}

func TestShellHookProducesShortLivedObserver(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"hook", "zsh"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hook command failed: %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "__observe-shell") || !strings.Contains(stdout.String(), "precmd") {
		t.Fatalf("unexpected shell hook: %s", stdout.String())
	}
}
