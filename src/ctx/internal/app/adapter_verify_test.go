package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	modpkg "github.com/webong/ext/pkg/plugin-go/engine/host/adapter"
)

func writeVerifyAdapter(t *testing.T, home, name string) *modpkg.Adapter {
	t.Helper()
	directory := filepath.Join(home, name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version = "2.0"
name = "` + name + `"
runtime = "computer"
surfaces = "shell"
executable = "ctx-` + name + `"
capabilities = "list,validate,run,doctor"
selector_key = "` + name + `"
commands = "` + name + `"
`
	if err := modpkg.WriteKeyValueManifest(directory, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "ctx-"+name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	loaded, err := modpkg.LoadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestAdapterVerifyCommand(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "state", "adapters")
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	t.Setenv("CTX_ADAPTER_HOME", home)
	t.Setenv("EXT_HOME", "")
	t.Setenv("EXT_ADAPTER_HOME", "")

	run := func(args ...string) (int, string) {
		var stdout, stderr bytes.Buffer
		code := adapterCommand(nil, append([]string{"verify"}, args...), &stdout, &stderr)
		return code, stdout.String() + stderr.String()
	}

	// Nothing installed and no store: passes quietly and creates nothing.
	if code, out := run(); code != 0 || out != "" {
		t.Fatalf("empty store: code=%d out=%q", code, out)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("verify created the adapter store")
	}

	store := adapterStore()
	alpha := writeVerifyAdapter(t, home, "alpha")
	writeVerifyAdapter(t, home, "bravo")
	if err := store.Trust(alpha); err != nil {
		t.Fatal(err)
	}

	code, out := run()
	if code != 1 || !strings.Contains(out, "alpha") || !strings.Contains(out, "trusted") ||
		!strings.Contains(out, "untrusted") || !strings.Contains(out, "ctx adapter trust bravo") {
		t.Fatalf("mixed store: code=%d out=%q", code, out)
	}
	if code, out := run("alpha"); code != 0 || strings.Contains(out, "untrusted") {
		t.Fatalf("trusted adapter by name: code=%d out=%q", code, out)
	}

	if err := os.WriteFile(alpha.ExecutablePath(), []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, out := run("alpha"); code != 1 || !strings.Contains(out, "changed") {
		t.Fatalf("tampered adapter: code=%d out=%q", code, out)
	}

	if code, out := run("missing"); code != 1 || !strings.Contains(out, "invalid") {
		t.Fatalf("missing adapter: code=%d out=%q", code, out)
	}
	// Verification must not have granted trust to anything.
	if ok, err := store.IsTrusted(mustLoad(t, store, "bravo")); err != nil || ok {
		t.Fatal("verify changed trust", err)
	}
}

func mustLoad(t *testing.T, store *modpkg.Store, name string) *modpkg.Adapter {
	t.Helper()
	loaded, err := store.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}
