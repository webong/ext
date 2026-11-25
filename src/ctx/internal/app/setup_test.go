package app

import (
	"bytes"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	modpkg "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
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

func TestCatalogAddInstallsDeclaredDependency(t *testing.T) { testCatalogDependency(t, false) }
func TestCatalogAddInstallsPackageDependency(t *testing.T)  { testCatalogDependency(t, true) }
func testCatalogDependency(t *testing.T, modern bool) {
	root := t.TempDir()
	t.Setenv("EXT_HOME", filepath.Join(root, "state"))
	t.Setenv("EXT_ADAPTER_HOME", filepath.Join(root, "state", "adapters"))
	t.Setenv("EXT_CATALOG_HOME", filepath.Join(root, "catalog"))
	t.Setenv("EXT_BIN_DIR", filepath.Join(root, "bin"))
	write := func(name, manifest string) {
		t.Helper()
		directory := filepath.Join(root, "catalog", name)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := modpkg.WriteKeyValueManifest(directory, manifest); err != nil {
			t.Fatal(err)
		}
		if modern && name == "chrome" {
			pm, err := modpkg.TemplateFromKeyValues(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var metadata map[string]any
			if err := json.Unmarshal(pm.Metadata[modpkg.MetadataKey], &metadata); err != nil {
				t.Fatal(err)
			}
			delete(metadata, "dependencies_"+runtime.GOOS)
			metadata["share_dependencies"] = []string{"credential:vault"}
			pm.Metadata[modpkg.MetadataKey], err = json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			pm.Requires = []packagekit.Dependency{{Provider: "vault", Contract: plugin.ContractRef{Name: plugin.AdapterContractName, Version: modpkg.APIVersion}, Operation: "share", OS: runtime.GOOS, Arch: runtime.GOARCH}}
			raw, err := json.MarshalIndent(pm, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "ext.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		command := "#!/bin/sh\nexit 0\n"
		if name == "chrome" {
			command = "#!/bin/sh\nprintf '%s\\n' \"$EXT_DEPENDENCY_CREDENTIAL\"\n"
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
		t.Setenv("EXT_DEPENDENCY_CREDENTIAL", "forged")
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
	t.Setenv("EXT_HOME", t.TempDir())
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

func TestCatalogWritersRespectPackageLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXT_HOME", home)
	t.Setenv("EXT_ADAPTER_HOME", "")
	lock := filepath.Join(home, ".package-write.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := addCatalogAdapter("unused"); err == nil || !strings.Contains(err.Error(), "writer unavailable") {
		t.Fatalf("add bypassed lock: %v", err)
	}
	if err := refreshCatalogAdapters(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "writer unavailable") {
		t.Fatalf("refresh bypassed lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "packages")); !os.IsNotExist(err) {
		t.Fatalf("blocked writer created package store: %v", err)
	}
}
