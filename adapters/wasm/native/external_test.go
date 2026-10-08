package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// isolateRuntimes hides every real WebAssembly runtime from the adapter, so a
// developer machine that has wasmtime installed does not change the result.
func isolateRuntimes(t *testing.T) string {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)
	return empty
}

// fakeRuntime installs a stand-in for a runtime on PATH. It answers --version,
// appends its arguments to a log, optionally sleeps or exits with a status, and
// prints a marker so a test can tell it ran.
func fakeRuntime(t *testing.T, directory, name, behavior string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake runtimes are shell scripts")
	}
	log := filepath.Join(directory, name+".log")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo \"" + name + " 9.9.9 (fake)\"; exit 0; fi\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + log + "'; done\n" +
		"printf -- '--end--\\n' >> '" + log + "'\n" + behavior + "\n"
	if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

func recorded(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// component and core are the smallest valid headers of each kind.
var (
	componentModule = []byte{0, 'a', 's', 'm', 0x0d, 0, 0x01, 0}
	coreModule      = smokeModule
)

func writeModule(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExternalRuntimesAreOfferedOnlyWhenInstalled(t *testing.T) {
	bin := isolateRuntimes(t)
	if code, stdout, _ := invoke("list"); code != 0 || stdout != "embedded\n" {
		t.Fatalf("with nothing installed: %d %q", code, stdout)
	}
	if code, _, stderr := invoke("validate", "wasmtime"); code != 127 || !strings.Contains(stderr, "not installed") || !strings.Contains(stderr, "embedded") {
		t.Fatalf("a missing runtime is 127 and says what is available: %d %q", code, stderr)
	}
	fakeRuntime(t, bin, "wasmtime", "exit 0")
	if code, stdout, _ := invoke("list"); code != 0 || stdout != "embedded\nwasmtime\n" {
		t.Fatalf("list: %d %q", code, stdout)
	}
	if code, _, _ := invoke("validate", "wasmtime"); code != 0 {
		t.Fatalf("validate: %d", code)
	}
	code, stdout, _ := invoke("observe")
	for _, want := range []string{`"selection":"wasmtime"`, `"version":"wasmtime 9.9.9 (fake)"`, `"kind":"wasm"`, `"isolation":"sandboxed"`, `"components":"yes"`} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("observe lacks %s: %d %q", want, code, stdout)
		}
	}
	if code, stdout, stderr := invoke("doctor", "wasmtime"); code != 0 || !strings.Contains(stdout, "wasmtime ready") {
		t.Fatalf("doctor: %d %q %q", code, stdout, stderr)
	}
}

func TestRuntimeInstalledByItsOwnInstallerIsFound(t *testing.T) {
	home := isolateRuntimes(t)
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	fakeRuntime(t, filepath.Join(home), "unused", "exit 0")
	directory := filepath.Join(home, ".wasmtime", "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeRuntime(t, directory, "wasmtime", "exit 0")
	if code, stdout, _ := invoke("list"); code != 0 || !strings.Contains(stdout, "wasmtime") {
		t.Fatalf("a runtime in its installer's directory must be found: %d %q", code, stdout)
	}
}

func TestWasmtimeArgumentsAreTranslated(t *testing.T) {
	bin := isolateRuntimes(t)
	log := fakeRuntime(t, bin, "wasmtime", "exit 0")
	module := writeModule(t, "m.wasm", coreModule)
	code, _, stderr := invoke("run", "wasmtime", "--", "--dir", "/data=/host/data", "--env", "B=2", "--env", "A=1", "--memory-pages", "10", module, "x", "-y")
	if code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	want := []string{"run", "--dir", "/host/data::/data", "--env", "A=1", "--env", "B=2", "-W", "max-memory-size=655360", module, "x", "-y", "--end--"}
	if got := recorded(t, log); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv\n got  %q\n want %q", got, want)
	}
}

func TestWasmerArgumentsAreTranslated(t *testing.T) {
	bin := isolateRuntimes(t)
	log := fakeRuntime(t, bin, "wasmer", "exit 0")
	module := writeModule(t, "m.wasm", coreModule)
	code, _, stderr := invoke("run", "wasmer", "--", "--dir", "/data=/host/data", "--env", "A=1", module, "x", "-y")
	if code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	want := []string{"run", "--volume", "/host/data:/data", "--env", "A=1", module, "--", "x", "-y", "--end--"}
	if got := recorded(t, log); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv\n got  %q\n want %q", got, want)
	}
}

func TestWhatARuntimeCannotEnforceIsRefusedNotIgnored(t *testing.T) {
	bin := isolateRuntimes(t)
	module := writeModule(t, "m.wasm", coreModule)
	for _, name := range []string{"wasmtime", "wasmer"} {
		log := fakeRuntime(t, bin, name, "exit 0")
		code, _, stderr := invoke("run", name, "--", "--ro-dir", "/data=/host/data", module)
		if code != 2 || !strings.Contains(stderr, "read-only") || !strings.Contains(stderr, "refused") {
			t.Errorf("%s --ro-dir: %d %q", name, code, stderr)
		}
		if len(recorded(t, log)) != 0 {
			t.Errorf("%s was started although the request could not be honored", name)
		}
	}
	log := fakeRuntime(t, bin, "wasmer", "exit 0")
	if code, _, stderr := invoke("run", "wasmer", "--", "--memory-pages", "4", module); code != 2 || !strings.Contains(stderr, "memory limit") {
		t.Errorf("wasmer --memory-pages: %d %q", code, stderr)
	}
	if len(recorded(t, log)) != 0 {
		t.Error("wasmer was started although it cannot limit memory")
	}
}

func TestComponentsGoToARuntimeThatHasTheComponentModel(t *testing.T) {
	bin := isolateRuntimes(t)
	component := writeModule(t, "c.wasm", componentModule)
	// With nothing installed the refusal says what to install and select.
	if code, _, stderr := invoke("run", "--", component); code != 126 || !strings.Contains(stderr, "Install wasmtime") {
		t.Fatalf("no runtime: %d %q", code, stderr)
	}
	log := fakeRuntime(t, bin, "wasmtime", "exit 0")
	fakeRuntime(t, bin, "wasmer", "exit 0")
	// No selection: the module decides and wasmtime runs it.
	if code, _, stderr := invoke("run", "--", component, "arg"); code != 0 {
		t.Fatalf("auto: %d %q", code, stderr)
	}
	if got := recorded(t, log); len(got) == 0 || got[0] != "run" {
		t.Fatalf("wasmtime was not used: %q", got)
	}
	// The built-in engine and wasmer cannot run components, and say so.
	for _, selection := range []string{"embedded", "wasmer"} {
		if code, _, stderr := invoke("run", selection, "--", component); code != 126 || !strings.Contains(stderr, "ctx set wasm wasmtime") {
			t.Errorf("%s: %d %q", selection, code, stderr)
		}
	}
	// --inspect reports which engine would run it.
	if code, stdout, _ := invoke("run", "--", "--inspect", component); code != 0 || !strings.Contains(stdout, `"runnable":true`) || !strings.Contains(stdout, `"engine":"wasmtime"`) {
		t.Fatalf("inspect: %d %q", code, stdout)
	}
	if code, stdout, _ := invoke("run", "embedded", "--", "--inspect", component); code != 0 || !strings.Contains(stdout, `"runnable":false`) {
		t.Fatalf("inspect with the built-in engine: %d %q", code, stdout)
	}
}

func TestACoreModuleStaysOnTheBuiltInEngineByDefault(t *testing.T) {
	bin := isolateRuntimes(t)
	log := fakeRuntime(t, bin, "wasmtime", "exit 0")
	module := writeModule(t, "m.wasm", coreModule)
	if code, _, stderr := invoke("run", "--", module); code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	if len(recorded(t, log)) != 0 {
		t.Fatal("having wasmtime installed must not change where an ordinary module runs")
	}
}

func TestExternalExitStatusPassesThroughAndTimeoutIsEnforcedHere(t *testing.T) {
	bin := isolateRuntimes(t)
	module := writeModule(t, "m.wasm", coreModule)
	fakeRuntime(t, bin, "wasmtime", "exit 7")
	if code, _, _ := invoke("run", "wasmtime", "--", module); code != 7 {
		t.Fatalf("exit status: %d", code)
	}
	fakeRuntime(t, bin, "wasmer", "/bin/sleep 30")
	started := time.Now()
	code, _, stderr := invoke("run", "wasmer", "--", "--timeout", "300ms", module)
	if code != 124 || !strings.Contains(stderr, "stopped wasmer") || time.Since(started) > 10*time.Second {
		t.Fatalf("timeout: %d %q after %s", code, stderr, time.Since(started))
	}
}

func TestWebModulesAreRefusedWhicheverEngineIsSelected(t *testing.T) {
	bin := isolateRuntimes(t)
	log := fakeRuntime(t, bin, "wasmtime", "exit 0")
	// (module (import "wbg" "x" (func))): JavaScript glue no runtime here has.
	web := []byte{0, 'a', 's', 'm', 1, 0, 0, 0, 1, 4, 1, 0x60, 0, 0, 2, 9, 1, 3, 'w', 'b', 'g', 1, 'x', 0, 0}
	module := writeModule(t, "web.wasm", web)
	for _, selection := range []string{"embedded", "wasmtime"} {
		if code, _, stderr := invoke("run", selection, "--", module); code != 126 || !strings.Contains(stderr, "web module") {
			t.Errorf("%s: %d %q", selection, code, stderr)
		}
	}
	if len(recorded(t, log)) != 0 {
		t.Error("a web module must never reach an external runtime")
	}
}
