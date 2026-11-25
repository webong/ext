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
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// TestAdapterAcrossProducts exercises the ext binary and installed product packages.
func TestAdapterAcrossProducts(t *testing.T) {
	native := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if native == "" {
		t.Skip("run scripts/package-acceptance.sh for product acceptance")
	}
	for _, key := range []string{"EXT_ADAPTER_HOME", "EXT_HOME"} {
		t.Setenv(key, "")
	}
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("EXT_HOME", home)
	t.Setenv("EXT_BIN_DIR", bin)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext := filepath.Join(bin, "ext"+suffix)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, native)
	for _, product := range []string{"ctx", "ctn", "cto"} {
		packageDir := filepath.Join(bin, "ext-packages", "ext%2f"+product)
		if err := os.MkdirAll(packageDir, 0o700); err != nil {
			t.Fatal(err)
		}
		executable := filepath.Join(packageDir, product+suffix)
		goBuild(t, executable, "../../src/"+product+"/plugin")
		manifest, err := packagekit.ExecutableManifest("ext/"+product, "dev", []plugin.Contract{pkgcmd.RunContract}, executable)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Descriptor.Identity.Revision = "command-v1"
		data, _ := json.Marshal(map[string]any{"commands": []pkgcmd.Declared{{Name: product, Transport: "socket"}}})
		manifest.Metadata = map[string]json.RawMessage{pkgcmd.MetadataKey: data}
		if err := store.WriteManifest(packageDir, manifest); err != nil {
			t.Fatal(err)
		}
	}
	source := t.TempDir()
	goBuild(t, filepath.Join(source, "evaluator"+suffix), "./testdata/evaluator")
	manifest := `api_version = "2.0"
name = "wasm"
runtime = "computer"
surfaces = "shell"
executable = "evaluator` + suffix + `"
capabilities = "list,observe,validate,run,run-verified,doctor"
engine = "evaluator"
role = "virtualizer"
supports = "wasm"
selector_key = "wasm"
computer_commands = "wasm"
self_contained = "true"
`
	if err := adapter.WriteKeyValueManifest(source, manifest); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "example.wasm")
	if err := os.WriteFile(file, []byte("fixture content"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	run := func(binary string, want int, contains string, args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = outside
		cmd.Env = append(acceptanceEnv(home), "EXT_BIN_DIR="+bin, "EXT_TEST_DOCTOR_FAIL="+os.Getenv("EXT_TEST_DOCTOR_FAIL"), "EXT_TEST_NO_EVIDENCE="+os.Getenv("EXT_TEST_NO_EVIDENCE"))
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		code := 0
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want || !strings.Contains(out.String(), contains) {
			t.Fatalf("%s %v: code %d, want %d; output %q, want %q", binary, args, code, want, out.String(), contains)
		}
		return out.String()
	}
	run(ext, 0, "bundle", "commands")
	run(ext, 2, "unknown command", "adapter")
	run(ext, 2, "usage: ext package", "package")
	run(ext, 0, "ext package trust wasm", "package", "install", source)
	run(ext, 0, "untrusted", "ctx", "adapter", "ls")
	run(ext, 0, "untrusted", "ctn", "adapter", "ls")
	// A separate product guest must respect the same package writer as the CLI.
	lockPath := filepath.Join(home, ".package-write.lock")
	if err := os.Mkdir(lockPath, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"ctx", "adapter", "install", source},
		{"ctx", "adapter", "trust", "wasm"},
		{"ctx", "adapter", "remove", "wasm"},
		{"ctx", "adapter", "add", "wasm"},
		{"ctx", "adapter", "refresh"},
	} {
		run(ext, 1, "writer unavailable", args...)
	}
	run(ext, 0, "untrusted", "ctx", "adapter", "ls")
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		t.Run("nested-adapter-snapshots", func(t *testing.T) {
			nativeBin := t.TempDir()
			tool := filepath.Join(nativeBin, "scopefixture")
			if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", nativeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

			for _, name := range []string{"snapshot_leaf", "snapshot_outer", "snapshot_browser"} {
				dir := t.TempDir()
				manifest := `api_version = "2.0"
name = "` + name + `"
runtime = "computer"
surfaces = "shell"
executable = "guest"
capabilities = "validate,doctor,share"
share_spaces = "credential"
selectable = "false"
self_contained = "true"
`
				if name == "snapshot_outer" {
					manifest = strings.Replace(manifest, `capabilities = "validate,doctor,share"`, `capabilities = "validate,doctor,share,run"`, 1)
					manifest += "computer_commands = \"scopefixture\"\n"
				}
				if name != "snapshot_leaf" {
					manifest += "dependencies_" + runtime.GOOS + " = \"credential:snapshot_leaf@2.0\"\n"
				}
				if name == "snapshot_browser" {
					manifest = strings.Replace(manifest, `runtime = "computer"`, `runtime = "browser"`, 1)
					manifest = strings.Replace(manifest, `surfaces = "shell"`, `surfaces = "web"`, 1)
					manifest = strings.Replace(manifest, `share_spaces = "credential"`, `share_spaces = "browser"`, 1)
					manifest += `browser_share = "cookie.query"
`
				}
				script := `#!/bin/sh
set -eu
printf '%s\n' "$0" >> "$EXT_HOME/snapshot-paths"
mkdir "$EXT_HOME/.package-write.lock"
rmdir "$EXT_HOME/.package-write.lock"
`
				script += "test \"${EXT_SNAPSHOT_LEAF_SELECTOR-}\" = fixture || exit 9\nprintf nested-value\n"
				if err := adapter.WriteKeyValueManifest(dir, manifest); err != nil {
					t.Fatal(err)
				}
				if name == "snapshot_leaf" {
					if err := os.WriteFile(filepath.Join(dir, "guest"), []byte(script), 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					goBuild(t, filepath.Join(dir, "guest"), "./testdata/credentialrelay")
				}

				run(ext, 0, "installed", "package", "install", dir)
				run(ext, 0, "trusted", "package", "trust", name)
			}
			run(ext, 0, "nested-value", "ctx", "credential", "get", "snapshot_outer:alpha", "--stdout")
			paths, err := os.ReadFile(filepath.Join(home, "snapshot-paths"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Fields(string(paths))
			if len(lines) != 2 {
				t.Fatalf("expected both invocations: %s", paths)
			}
			for _, path := range lines {
				if !strings.Contains(path, "ext-command-graph-") {
					t.Fatalf("executed installed adapter: %s", path)
				}
				if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatalf("snapshot retained: %s: %v", path, err)
				}
			}
			if err := os.Remove(filepath.Join(home, "snapshot-paths")); err != nil {
				t.Fatal(err)
			}
			run(ext, 0, "nested-value", "ctx", "share:browser", "cookie", "query", "--from", "snapshot_browser:default", "--site", "https://example.test", "--stdout", "--strict")
			paths, err = os.ReadFile(filepath.Join(home, "snapshot-paths"))
			if err != nil {
				t.Fatal(err)
			}
			lines = strings.Fields(string(paths))
			if len(lines) != 2 {
				t.Fatalf("browser dependency calls: %s", paths)
			}
			for _, path := range lines {
				if !strings.Contains(path, "ext-command-graph-") {
					t.Fatalf("browser executed original: %s", path)
				}
				if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatalf("browser snapshot retained: %v", err)
				}
			}
			run(ext, 0, "removed", "package", "remove", "snapshot_browser")
			run(ext, 0, "removed", "package", "remove", "snapshot_outer")
			run(ext, 0, "removed", "package", "remove", "snapshot_leaf")
		})
	}
	run(ext, 3, "UNSUPPORTED", "ctn", "verify", file)
	run(ext, 1, "not trusted", "package", "doctor", "wasm")
	run(ext, 0, "trusted wasm", "package", "trust", "wasm")
	run(ext, 0, "evaluator doctor", "package", "doctor", "wasm")
	t.Setenv("EXT_TEST_DOCTOR_FAIL", "1")
	run(ext, 7, "evaluator doctor", "package", "doctor", "wasm")
	t.Setenv("EXT_TEST_DOCTOR_FAIL", "")
	run(ext, 0, "trusted", "package", "verify", "wasm")
	t.Setenv("EXT_TEST_NO_EVIDENCE", "1")
	run(ext, 3, "no sandbox evidence", "ctn", "verify", file)
	t.Setenv("EXT_TEST_NO_EVIDENCE", "")
	run(ext, 0, "PASS", "ctn", "verify", file)
	run(ext, 0, "PASS", "ctn", "verify", file)

	// Adapter-specific removal must also protect ordinary package requirements.
	if native := os.Getenv("EXT_ACCEPTANCE_LIBRARY"); native != "" {
		stageAcceptanceEngine(t, bin, native)
		consumer := t.TempDir()
		if err := os.WriteFile(filepath.Join(consumer, "data"), []byte("adapter consumer"), 0600); err != nil {
			t.Fatal(err)
		}
		cm, err := packagekit.SourceManifest("example/adapter-consumer", "1.0.0", []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.consumer", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}, consumer, []string{"data"})
		if err != nil {
			t.Fatal(err)
		}
		cm.Requires = []packagekit.Dependency{{Contract: plugin.ContractRef{Name: plugin.AdapterContractName, Version: adapter.APIVersion}, Operation: "run"}}
		if err := store.WriteManifest(consumer, cm); err != nil {
			t.Fatal(err)
		}
		run(ext, 0, "installed", "package", "install", consumer)
		run(ext, 1, "removal leaves unresolved dependencies", "package", "remove", "wasm")
		run(ext, 0, "trusted=true", "package", "verify", "wasm")
		run(ext, 0, "PASS", "ctn", "verify", file)
		run(ext, 0, "removed adapter wasm", "package", "remove", "wasm", "--force")
		run(ext, 1, `"status":"not_found"`, "package", "plan")
		run(ext, 0, "installed", "package", "install", source)
		run(ext, 0, "removed", "package", "remove", "example/adapter-consumer")
		run(ext, 0, "trusted wasm", "package", "trust", "wasm")
	}
	// Products consume the current shared store; the migration command is gone.
	run(ext, 2, "usage: ext package", "package", "migrate")
	run(ext, 0, "PASS", "ctn", "verify", file)
	run(ext, 0, "trusted", "ctx", "adapter", "ls")
	installed := filepath.Join(home, "packages", "wasm", "evaluator"+suffix)
	f, err := os.OpenFile(installed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("tampered")); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	run(ext, 1, "wasm", "package", "verify", "wasm")
	run(ext, 3, "UNSUPPORTED", "ctn", "verify", file)
	run(ext, 0, "removed adapter wasm", "package", "remove", "wasm")
	if out := run(ext, 0, "", "ctn", "adapter", "ls"); out != "" {
		t.Fatalf("adapter still listed: %s", out)
	}
}
