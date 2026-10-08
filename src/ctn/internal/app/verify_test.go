package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/plugin/adapter"
)

// engineStore installs a fake engine adapter into a temporary shared store and
// returns the path of a log that records every operation the engine receives.
func engineStore(t *testing.T) (string, func(name, kind, observe, run string, trust bool) string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake engines are shell scripts")
	}
	home := t.TempDir()
	for _, key := range []string{"EXT_ADAPTER_HOME", "CTX_HOME", "CTX_ADAPTER_HOME"} {
		t.Setenv(key, "")
	}
	t.Setenv("EXT_HOME", home)
	install := func(name, kind, observe, run string, trust bool) string {
		t.Helper()
		directory := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		log := filepath.Join(home, name+".log")
		manifest := "api_version = \"2.0\"\nname = \"" + name + "\"\nruntime = \"computer\"\nsurfaces = \"shell\"\nexecutable = \"ctx-" + name + "\"\n" +
			"capabilities = \"list,observe,validate,run,doctor\"\nsupports = \"engine," + kind + "\"\nselector_key = \"" + kind + "\"\n" +
			"computer_commands = \"" + name + "\"\nself_contained = \"true\"\n"
		script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\ncase \"$1\" in\n" +
			"observe) echo '" + observe + "'; exit 0;;\nrun) " + run + ";;\n*) exit 0;;\nesac\n"
		if err := os.WriteFile(filepath.Join(directory, "adapter.toml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "ctx-"+name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		store := adapter.NewStore(adapter.Home())
		installed, err := store.Install(directory)
		if err != nil {
			t.Fatal(err)
		}
		if trust {
			if err := store.Trust(installed); err != nil {
				t.Fatal(err)
			}
		}
		return log
	}
	return home, install
}

const (
	sandboxed = `{"version":1,"contexts":[{"selection":"embedded","attributes":{"kind":"wasm","version":"1","isolation":"sandboxed"}}]}`
	hostOnly  = `{"version":1,"contexts":[{"selection":"21","attributes":{"kind":"wasm","version":"1","isolation":"host"}}]}`
	mixed     = `{"version":1,"contexts":[{"selection":"embedded","attributes":{"isolation":"sandboxed"}},{"selection":"loose","attributes":{"isolation":"host"}}]}`
)

func content(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func verify(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append([]string{"verify"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func ran(log string) bool {
	data, err := os.ReadFile(log)
	return err == nil && strings.Contains(string(data), "run ")
}

func TestVerifyPassesContentThatRunsCleanlyInASandbox(t *testing.T) {
	_, install := engineStore(t)
	log := install("wasm", "wasm", sandboxed, "exit 0", true)
	file := content(t, "ok.wasm", "payload")
	code, stdout, stderr := verify(t, "--timeout", "10s", file)
	if code != exitPass || !strings.HasPrefix(stdout, "PASS ") || !strings.Contains(stdout, "isolation=sandboxed") || !strings.Contains(stdout, "engine=wasm") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	// sha256("payload"), so the verdict names exactly what was checked.
	if !strings.Contains(stdout, "239f59ed55e7") {
		t.Fatalf("the digest is missing: %q", stdout)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "run -- --timeout 10s "+file) {
		t.Fatalf("the engine got %q", data)
	}
}

func TestVerifyJSONCarriesTheVerdictAndTheDigest(t *testing.T) {
	_, install := engineStore(t)
	install("wasm", "wasm", sandboxed, "exit 0", true)
	code, stdout, _ := verify(t, "--json", content(t, "a.wasm", "payload"))
	var v verdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil || code != 0 || v.Verdict != "PASS" || v.Isolation != "sandboxed" ||
		!strings.HasPrefix(v.SHA256, "239f59ed55e7") || len(v.SHA256) != 64 {
		t.Fatalf("%d %+v %v", code, v, err)
	}
}

func TestVerifyReportsContentThatFailsWithItsOwnExitStatus(t *testing.T) {
	_, install := engineStore(t)
	install("wasm", "wasm", sandboxed, "echo boom >&2; exit 7", true)
	code, stdout, _ := verify(t, content(t, "bad.wasm", "x"))
	if code != exitFail || !strings.HasPrefix(stdout, "FAIL ") || !strings.Contains(stdout, "boom") {
		t.Fatalf("%d %q", code, stdout)
	}
	code, stdout, _ = verify(t, "--json", content(t, "bad2.wasm", "x"))
	var v verdict
	if json.Unmarshal([]byte(stdout), &v); v.ExitCode != 7 || code != exitFail {
		t.Fatalf("the content's own status must be reported: %d %+v", code, v)
	}
}

func TestVerifyNeverRunsContentInAnEngineThatDoesNotConfineIt(t *testing.T) {
	for name, observe := range map[string]string{"host": hostOnly, "mixed without a selection": mixed, "no isolation reported": `{"version":1,"contexts":[{"selection":"a","attributes":{}}]}`, "garbage": "not json"} {
		_, install := engineStore(t)
		log := install("wasm", "wasm", observe, "exit 0", true)
		code, stdout, _ := verify(t, content(t, "x.wasm", "x"))
		if code != exitUnverified || !strings.HasPrefix(stdout, "UNSUPPORTED ") {
			t.Errorf("%s: %d %q", name, code, stdout)
		}
		if ran(log) {
			t.Errorf("%s: the engine was run although it does not confine its content", name)
		}
	}
}

func TestVerifyChecksTheSelectedContextOnly(t *testing.T) {
	_, install := engineStore(t)
	log := install("wasm", "wasm", mixed, "exit 0", true)
	if code, stdout, _ := verify(t, "--selection", "embedded", content(t, "x.wasm", "x")); code != exitPass {
		t.Fatalf("a sandboxed selection of a mixed engine may run: %d %q", code, stdout)
	}
	if !ran(log) {
		t.Fatal("the engine did not run")
	}
	if code, _, _ := verify(t, "--selection", "loose", content(t, "y.wasm", "y")); code != exitUnverified {
		t.Fatalf("the host-isolated selection must not run: %d", code)
	}
	if code, _, _ := verify(t, "--selection", "missing", content(t, "z.wasm", "z")); code != exitUnverified {
		t.Fatalf("an unknown selection must not run: %d", code)
	}
}

func TestVerifyRefusesAnUntrustedEngine(t *testing.T) {
	_, install := engineStore(t)
	log := install("wasm", "wasm", sandboxed, "exit 0", false)
	code, stdout, _ := verify(t, content(t, "x.wasm", "x"))
	if code != exitUnverified || !strings.Contains(stdout, "not trusted") || ran(log) {
		t.Fatalf("%d %q ran=%v", code, stdout, ran(log))
	}
}

func TestVerifyTreatsAnEngineThatCannotRunTheInputAsUnverifiedNotFailed(t *testing.T) {
	_, install := engineStore(t)
	install("wasm", "wasm", sandboxed, "echo 'is a web module' >&2; exit 126", true)
	code, stdout, _ := verify(t, content(t, "web.wasm", "x"))
	if code != exitUnverified || !strings.HasPrefix(stdout, "UNSUPPORTED ") || !strings.Contains(stdout, "web module") {
		t.Fatalf("%d %q", code, stdout)
	}
}

func TestVerifyReportsATimeout(t *testing.T) {
	_, install := engineStore(t)
	install("wasm", "wasm", sandboxed, "exit 124", true)
	code, stdout, _ := verify(t, "--timeout", "1s", content(t, "slow.wasm", "x"))
	if code != exitTimeout || !strings.HasPrefix(stdout, "TIMEOUT ") {
		t.Fatalf("%d %q", code, stdout)
	}
}

func TestVerifyChoosesTheEngineByExtensionAndNamesTheFileForEvm(t *testing.T) {
	_, install := engineStore(t)
	wasmLog := install("wasm", "wasm", sandboxed, "exit 0", true)
	evmLog := install("evm", "evm", `{"version":1,"contexts":[{"selection":"prague","attributes":{"isolation":"sandboxed"}}]}`, "exit 0", true)
	hexFile := content(t, "code.hex", "602a")
	if code, stdout, _ := verify(t, hexFile); code != exitPass || !strings.Contains(stdout, "engine=evm") {
		t.Fatalf("%d %q", code, stdout)
	}
	data, _ := os.ReadFile(evmLog)
	if !strings.Contains(string(data), "-- --timeout 30s @"+hexFile) {
		t.Fatalf("the evm engine reads a file given as @path: %q", data)
	}
	if ran(wasmLog) {
		t.Fatal("the wasm engine must not run evm content")
	}
	if code, _, stderr := verify(t, content(t, "notes.txt", "x")); code != exitUsage || !strings.Contains(stderr, "--engine") {
		t.Fatalf("an unknown extension needs --engine: %d %q", code, stderr)
	}
	if code, _, _ := verify(t, "--engine", "wasm", content(t, "notes.bin", "x")); code != exitPass {
		t.Fatalf("--engine overrides the extension: %d", code)
	}
}

func TestVerifyUsageAndMissingEngine(t *testing.T) {
	engineStore(t)
	if code, _, stderr := verify(t); code != exitUsage || !strings.Contains(stderr, "usage") {
		t.Fatalf("%d %q", code, stderr)
	}
	if code, _, _ := verify(t, filepath.Join(t.TempDir(), "absent.wasm")); code != exitUsage {
		t.Fatalf("a missing file: %d", code)
	}
	if code, _, stderr := verify(t, content(t, "x.wasm", "x")); code != exitUsage || !strings.Contains(stderr, "no engine adapter") {
		t.Fatalf("no engine installed: %d %q", code, stderr)
	}
	if code, _, _ := verify(t, "--timeout", "0s", content(t, "y.wasm", "y")); code != exitUsage {
		t.Fatalf("a zero timeout: %d", code)
	}
}
