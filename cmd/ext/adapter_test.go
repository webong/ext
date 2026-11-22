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
	run := func(binary string, want int, contains string, args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
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
	run(ext, 0, "adapter", "commands")
	run(ext, 0, "builtin", "which", "adapter")
	run(ext, 2, "ext: adapter requires", "adapter")
	run(ext, 0, "ext adapter trust wasm", "adapter", "install", source)
	run(ext, 0, "untrusted", "ctx", "adapter", "ls")
	run(ext, 0, "untrusted", "ctn", "adapter", "ls")
	run(ext, 3, "UNSUPPORTED", "ctn", "verify", file)
	run(ext, 1, "not trusted", "adapter", "doctor", "wasm")
	run(ext, 0, "trusted adapter wasm", "ctx", "adapter", "trust", "wasm")
	run(ext, 0, "evaluator doctor", "adapter", "doctor", "wasm")
	t.Setenv("EXT_TEST_DOCTOR_FAIL", "1")
	run(ext, 7, "evaluator doctor", "adapter", "doctor", "wasm")
	t.Setenv("EXT_TEST_DOCTOR_FAIL", "")
	run(ext, 0, "trusted", "adapter", "verify", "wasm")
	t.Setenv("EXT_TEST_NO_EVIDENCE", "1")
	run(ext, 3, "no sandbox evidence", "ctn", "verify", file)
	t.Setenv("EXT_TEST_NO_EVIDENCE", "")
	run(ext, 0, "PASS", "ctn", "verify", file)
	run(ext, 0, "PASS", "ctn", "verify", file)
	installed := filepath.Join(home, "adapters", "wasm", "evaluator"+suffix)
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
	run(ext, 1, "wasm", "adapter", "verify", "wasm")
	run(ext, 3, "UNSUPPORTED", "ctn", "verify", file)
	run(ext, 0, "removed adapter wasm", "ctx", "adapter", "remove", "wasm")
	if out := run(ext, 0, "", "adapter", "ls"); out != "" {
		t.Fatalf("adapter still listed: %s", out)
	}
}
