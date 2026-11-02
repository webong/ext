package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/host/adapter"
)

// TestAdapterAcrossProducts exercises real binaries against one isolated home.
// No CTX process participates in standalone CTN's evaluator invocation.
func TestAdapterAcrossProducts(t *testing.T) {
	for _, key := range []string{"EXT_ADAPTER_HOME", "CTX_ADAPTER_HOME", "CTX_HOME"} {
		t.Setenv(key, "")
	}
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("EXT_HOME", home)
	t.Setenv("CTX_BIN_DIR", bin)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext, ctx, ctn := filepath.Join(bin, "ext"+suffix), filepath.Join(bin, "ctx"+suffix), filepath.Join(bin, "ctn"+suffix)
	goBuild(t, ext, ".")
	goBuild(t, ctx, "../../src/ctx/cmd/ctx")
	goBuild(t, ctn, "../../src/ctn/cmd/ctn")
	source := t.TempDir()
	goBuild(t, filepath.Join(source, "evaluator"+suffix), "./testdata/evaluator")
	manifest := `api_version = "2.0"
name = "wasm"
runtime = "computer"
surfaces = "shell"
executable = "evaluator` + suffix + `"
capabilities = "list,observe,validate,run,doctor"
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
	run(ctx, 0, "untrusted", "adapter", "ls")
	run(ctn, 0, "untrusted", "adapter", "ls")
	run(ctn, 3, "UNSUPPORTED", "verify", file)
	run(ext, 1, "not trusted", "adapter", "doctor", "wasm")
	run(ctx, 0, "trusted adapter wasm", "adapter", "trust", "wasm")
	run(ext, 0, "evaluator doctor", "adapter", "doctor", "wasm")
	t.Setenv("EXT_TEST_DOCTOR_FAIL", "1")
	run(ext, 7, "evaluator doctor", "adapter", "doctor", "wasm")
	t.Setenv("EXT_TEST_DOCTOR_FAIL", "")
	run(ext, 0, "trusted", "adapter", "verify", "wasm")
	run(ctn, 0, "PASS", "verify", file)
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
	run(ctn, 3, "UNSUPPORTED", "verify", file)
	run(ctx, 0, "removed adapter wasm", "adapter", "remove", "wasm")
	if out := run(ext, 0, "", "adapter", "ls"); out != "" {
		t.Fatalf("adapter still listed: %s", out)
	}
}
