package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	modpkg "github.com/webong/ext/ctx/internal/mod"
)

func TestParseAdapterSelectionNamesAndNumbers(t *testing.T) {
	available := []*modpkg.Adapter{
		{Manifest: modpkg.Manifest{Name: "docker"}},
		{Manifest: modpkg.Manifest{Name: "kube"}},
		{Manifest: modpkg.Manifest{Name: "firefox"}},
	}
	got, err := parseAdapterSelection("2,docker,2", available)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kube", "docker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selection = %#v, want %#v", got, want)
	}
}

func TestCatalogAddInstallsDeclaredDependency(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	t.Setenv("CTX_ADAPTER_HOME", filepath.Join(root, "state", "adapters"))
	t.Setenv("CTX_CATALOG_HOME", filepath.Join(root, "catalog"))
	t.Setenv("CTX_BIN_DIR", filepath.Join(root, "bin"))
	write := func(name, manifest string) {
		t.Helper()
		directory := filepath.Join(root, "catalog", name)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "adapter.toml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		command := "#!/bin/sh\nexit 0\n"
		if name == "chrome" {
			command = "#!/bin/sh\nprintf '%s\\n' \"$CTX_DEPENDENCY_CREDENTIAL\"\n"
		}
		if err := os.WriteFile(filepath.Join(directory, "ctx-"+name), []byte(command), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write("vault", `api_version = "2.0"
name = "vault"
runtime = "computer"
surfaces = "shell"
executable = "ctx-vault"
capabilities = "validate,doctor,share"
share_spaces = "credential"
selectable = "false"
self_contained = "true"
`)
	write("chrome", `api_version = "2.0"
name = "chrome"
runtime = "browser"
surfaces = "web"
executable = "ctx-chrome"
capabilities = "validate,doctor,share"
selector_key = "browser"
share_spaces = "browser"
dependencies_`+runtime.GOOS+` = "credential:vault@2.0"
`)
	installed, err := addCatalogAdapter("chrome")
	if err != nil {
		t.Fatal(err)
	}
	if installed.Manifest.Dependencies["credential"].Adapter != "vault" {
		t.Fatal("browser dependency declaration was lost")
	}
	vault, err := adapterStore().Load("vault")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapterStore().AssertTrusted(vault); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vault.ExecutablePath(), []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := refreshCatalogAdapters(&bytes.Buffer{}); err != nil {
		t.Fatalf("refresh did not repair dependency before browser: %v", err)
	}
	vault, err = adapterStore().Load("vault")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapterStore().AssertTrusted(vault); err != nil {
		t.Fatalf("refreshed dependency is not trusted: %v", err)
	}
	var removalOutput, removalErrors bytes.Buffer
	resolver, err := newResolver()
	if err != nil {
		t.Fatal(err)
	}
	if code := adapterCommand(resolver, []string{"remove", "vault"}, &removalOutput, &removalErrors); code == 0 {
		t.Fatal("removed a store adapter still required by the browser")
	}
	if runtime.GOOS != "windows" {
		t.Setenv("CTX_DEPENDENCY_CREDENTIAL", "forged")
		var stdout, stderr bytes.Buffer
		if code := invokeAdapterIO(resolver, installed, "share", "", []string{"cookie", "read"}, "", bytes.NewReader(nil), &stdout, &stderr); code != 0 || stdout.String() != "vault\n" {
			t.Fatalf("dependency environment code=%d output=%q diagnostics=%q", code, stdout.String(), stderr.String())
		}
	}
	removalOutput.Reset()
	removalErrors.Reset()
	if code := adapterCommand(resolver, []string{"remove", "vault", "--force"}, &removalOutput, &removalErrors); code != 0 {
		t.Fatalf("forced dependency removal code=%d diagnostics=%q", code, removalErrors.String())
	}
}

func TestSetupMinimalDoesNotRequireCatalog(t *testing.T) {
	t.Setenv("CTX_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := setupCommand([]string{"--minimal"}, bytes.NewReader(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("setup minimal returned %d: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "No new adapters selected. Existing adapters were preserved.\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestParseAdapterSelectionRejectsUnknown(t *testing.T) {
	available := []*modpkg.Adapter{{Manifest: modpkg.Manifest{Name: "docker"}}}
	if _, err := parseAdapterSelection("unknown", available); err == nil {
		t.Fatal("unknown adapter was accepted")
	}
}
