package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
)

// evaluatorStore installs a fake evaluator adapter into a temporary shared store and
// returns the path of a log that records every operation the evaluator receives.
func evaluatorStore(t *testing.T) (string, func(name, kind, observe, run string, trust bool, evidence ...string) string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake evaluators are shell scripts")
	}
	home := t.TempDir()
	for _, key := range []string{"EXT_ADAPTER_HOME", "EXT_HOME"} {
		t.Setenv(key, "")
	}
	t.Setenv("EXT_HOME", home)
	install := func(name, kind, observe, run string, trust bool, evidence ...string) string {
		t.Helper()
		directory := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		log := filepath.Join(home, name+".log")
		manifest := "api_version = \"2.0\"\nname = \"" + name + "\"\nruntime = \"computer\"\nsurfaces = \"shell\"\nexecutable = \"ctx-" + name + "\"\n" +
			"capabilities = \"list,observe,validate,run,run-verified,doctor\"\nengine = \"evaluator\"\nrole = \"virtualizer\"\nsupports = \"" + kind + "\"\nselector_key = \"" + kind + "\"\n" +
			"computer_commands = \"" + name + "\"\nself_contained = \"true\"\n"
		report := validRunEvidence
		if len(evidence) != 0 {
			report = evidence[0]
		}
		gate := "exit 0"
		if report != "" {
			gate = "printf '%s\\n' '" + report + "'; IFS= read -r response && [ \"$response\" = run ] || exit 126; " +
				"echo executed >> '" + log + "'; " + run
		}
		script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\ncase \"$1\" in\n" +
			"observe) echo '" + observe + "'; exit 0;;\nrun-verified) " + gate + ";;\n*) exit 0;;\nesac\n"
		if err := adapter.WriteKeyValueManifest(directory, manifest); err != nil {
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
	sandboxed        = `{"version":1,"contexts":[{"selection":"embedded","attributes":{"kind":"wasm","version":"1","isolation":"sandboxed","boundary":"runtime"}}]}`
	hostOnly         = `{"version":1,"contexts":[{"selection":"21","attributes":{"kind":"wasm","version":"1","isolation":"host","boundary":"process"}}]}`
	validRunEvidence = `{"boundary":"runtime","driver":"fixture","grant":{},"enforced":["filesystem","network.none","process","devices","environment","lifecycle.fresh"]}`
)

func contentFile(t *testing.T, name, data string) string {
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
	return err == nil && strings.Contains(string(data), "\nexecuted\n")
}

func TestVerifyPassesContentThatRunsCleanlyInASandbox(t *testing.T) {
	_, install := evaluatorStore(t)
	log := install("wasm", "wasm", hostOnly, "exit 0", true)
	file := contentFile(t, "ok.wasm", "payload")
	code, stdout, stderr := verify(t, "--timeout", "10s", file)
	if code != exitPass || !strings.HasPrefix(stdout, "PASS ") || !strings.Contains(stdout, "isolation=sandboxed") || !strings.Contains(stdout, "boundary=runtime") || !strings.Contains(stdout, "engine=wasm") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	// sha256("payload"), so the verdict names exactly what was checked.
	if !strings.Contains(stdout, "239f59ed55e7") {
		t.Fatalf("the digest is missing: %q", stdout)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "run-verified -- --timeout 10s -- "+file+"\nexecuted\n") || strings.Contains(string(data), "observe") {
		t.Fatalf("the evaluator got %q", data)
	}
}

func TestVerifyJSONCarriesTheVerdictAndTheDigest(t *testing.T) {
	_, install := evaluatorStore(t)
	install("wasm", "wasm", sandboxed, "exit 0", true)
	code, stdout, _ := verify(t, "--json", contentFile(t, "a.wasm", "payload"))
	var v verdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil || code != 0 || v.Verdict != "PASS" || v.Isolation != "sandboxed" ||
		v.Sandbox == nil || v.Sandbox.Driver != "fixture" ||
		!strings.HasPrefix(v.SHA256, "239f59ed55e7") || len(v.SHA256) != 64 {
		t.Fatalf("%d %+v %v", code, v, err)
	}
}

func TestVerifyReportsContentThatFailsWithItsOwnExitStatus(t *testing.T) {
	_, install := evaluatorStore(t)
	install("wasm", "wasm", sandboxed, "echo boom >&2; exit 7", true)
	code, stdout, _ := verify(t, contentFile(t, "bad.wasm", "x"))
	if code != exitFail || !strings.HasPrefix(stdout, "FAIL ") || !strings.Contains(stdout, "boom") {
		t.Fatalf("%d %q", code, stdout)
	}
	code, stdout, _ = verify(t, "--json", contentFile(t, "bad2.wasm", "x"))
	var v verdict
	if json.Unmarshal([]byte(stdout), &v); v.ExitCode != 7 || code != exitFail {
		t.Fatalf("the content's own status must be reported: %d %+v", code, v)
	}
}

func TestVerifyRefusesInsufficientRunnerEvidence(t *testing.T) {
	for _, test := range []struct{ name, report, reason string }{
		{"discovery claim without evidence", "", "no sandbox evidence"},
		{"malformed report", "{not JSON}", "malformed admission report"},
		{"missing grant", strings.Replace(validRunEvidence, `"grant":{},`, "", 1), "malformed admission report"},
		{"unknown boundary", strings.Replace(validRunEvidence, `"runtime"`, `"vm"`, 1), "malformed admission report"},
		{"plain process", strings.Replace(validRunEvidence, `"runtime"`, `"process"`, 1), "do not meet sandbox requirements"},
		{"missing required class", strings.Replace(validRunEvidence, `"filesystem",`, "", 1), "malformed admission report"},
		{"unenforced required class", strings.Replace(validRunEvidence, `"enforced":["filesystem",`, `"unenforced":["filesystem"],"enforced":[`, 1), "do not meet sandbox requirements"},
		{"unrequested host access", strings.Replace(validRunEvidence, `"grant":{}`, `"grant":{"process":{"children":true}}`, 1), "exceeds verification policy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, install := evaluatorStore(t)
			log := install("wasm", "wasm", sandboxed, "exit 0", true, test.report)
			code, stdout, _ := verify(t, contentFile(t, "x.wasm", "x"))
			if code != exitUnverified || !strings.Contains(stdout, test.reason) || ran(log) {
				t.Fatalf("%d %q ran=%v", code, stdout, ran(log))
			}
		})
	}
}

func TestVerifyPassesSelectionToTheRunner(t *testing.T) {
	_, install := evaluatorStore(t)
	log := install("wasm", "wasm", hostOnly, "exit 0", true)
	code, stdout, _ := verify(t, "--selection", "embedded", contentFile(t, "x.wasm", "x"))
	data, err := os.ReadFile(log)
	if code != exitPass || err != nil || !strings.HasPrefix(string(data), "run-verified embedded -- ") || !ran(log) {
		t.Fatalf("%d %q log=%q err=%v", code, stdout, data, err)
	}
}

func TestVerifyRefusesAnUntrustedEvaluator(t *testing.T) {
	_, install := evaluatorStore(t)
	log := install("wasm", "wasm", sandboxed, "exit 0", false)
	code, stdout, _ := verify(t, contentFile(t, "x.wasm", "x"))
	if _, err := os.Stat(log); code != exitUnverified || !strings.Contains(stdout, "not trusted") || !os.IsNotExist(err) {
		t.Fatalf("%d %q log stat=%v", code, stdout, err)
	}
}

func TestVerifyTreatsAnEvaluatorThatCannotRunTheInputAsUnverifiedNotFailed(t *testing.T) {
	_, install := evaluatorStore(t)
	install("wasm", "wasm", sandboxed, "echo 'is a web module' >&2; exit 126", true)
	code, stdout, _ := verify(t, contentFile(t, "web.wasm", "x"))
	if code != exitUnverified || !strings.HasPrefix(stdout, "UNSUPPORTED ") || !strings.Contains(stdout, "web module") {
		t.Fatalf("%d %q", code, stdout)
	}
}

func TestVerifyReportsATimeout(t *testing.T) {
	_, install := evaluatorStore(t)
	install("wasm", "wasm", sandboxed, "exit 124", true)
	code, stdout, _ := verify(t, "--timeout", "1s", contentFile(t, "slow.wasm", "x"))
	if code != exitTimeout || !strings.HasPrefix(stdout, "TIMEOUT ") {
		t.Fatalf("%d %q", code, stdout)
	}
}

func TestVerifyChoosesTheEvaluatorByExtensionAndNamesTheFileForEvm(t *testing.T) {
	_, install := evaluatorStore(t)
	wasmLog := install("wasm", "wasm", sandboxed, "exit 0", true)
	evmLog := install("evm", "evm", `{"version":1,"contexts":[{"selection":"prague","attributes":{"isolation":"sandboxed","boundary":"runtime"}}]}`, "exit 0", true)
	hexFile := contentFile(t, "code.hex", "602a")
	if code, stdout, _ := verify(t, hexFile); code != exitPass || !strings.Contains(stdout, "engine=evm") {
		t.Fatalf("%d %q", code, stdout)
	}
	data, _ := os.ReadFile(evmLog)
	if !strings.Contains(string(data), "-- @"+hexFile) {
		t.Fatalf("the evm evaluator reads a file given as @path: %q", data)
	}
	if ran(wasmLog) {
		t.Fatal("the wasm evaluator must not run evm content")
	}
	if code, _, stderr := verify(t, contentFile(t, "notes.txt", "x")); code != exitUsage || !strings.Contains(stderr, "--engine") {
		t.Fatalf("an unknown extension needs --engine: %d %q", code, stderr)
	}
	if code, _, _ := verify(t, "--engine", "wasm", contentFile(t, "notes.bin", "x")); code != exitPass {
		t.Fatalf("--engine overrides the extension: %d", code)
	}
}

func TestVerifyUsageAndMissingEvaluator(t *testing.T) {
	evaluatorStore(t)
	if code, _, stderr := verify(t); code != exitUsage || !strings.Contains(stderr, "usage") {
		t.Fatalf("%d %q", code, stderr)
	}
	if code, _, _ := verify(t, filepath.Join(t.TempDir(), "absent.wasm")); code != exitUsage {
		t.Fatalf("a missing file: %d", code)
	}
	if code, _, stderr := verify(t, contentFile(t, "x.wasm", "x")); code != exitUsage || !strings.Contains(stderr, "no evaluator adapter") {
		t.Fatalf("no evaluator installed: %d %q", code, stderr)
	}
	if code, _, _ := verify(t, "--timeout", "0s", contentFile(t, "y.wasm", "y")); code != exitUsage {
		t.Fatalf("a zero timeout: %d", code)
	}
}
