package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const guest = `package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	switch os.Args[1] {
	case "env":
		fmt.Printf("home=%q foo=%q", os.Getenv("HOME"), os.Getenv("FOO"))
	case "exit":
		code, _ := strconv.Atoi(os.Args[2])
		os.Exit(code)
	case "read":
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Print("denied")
			os.Exit(3)
		}
		fmt.Printf("read=%s", data)
	case "spin":
		for {
		}
	}
}
`

func buildGuest(t *testing.T) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain is required to build the WASI test guest")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte(guest), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "guest.wasm")
	build := exec.Command(goTool, "build", "-o", output, source)
	build.Dir = directory
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOFLAGS=-mod=mod", "GO111MODULE=off")
	if message, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, message)
	}
	return output
}

func invoke(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestDiscoveryOperations(t *testing.T) {
	if code, stdout, _ := invoke("list"); code != 0 || stdout != "embedded\n" {
		t.Fatalf("list: %d %q", code, stdout)
	}
	if code, stdout, _ := invoke("observe"); code != 0 || !strings.Contains(stdout, `"selection":"embedded"`) {
		t.Fatalf("observe: %d %q", code, stdout)
	}
	if code, _, _ := invoke("validate", "embedded"); code != 0 {
		t.Fatalf("validate embedded: %d", code)
	}
	if code, _, stderr := invoke("validate", "wasmtime"); code != 1 || !strings.Contains(stderr, "not available") {
		t.Fatalf("validate unknown: %d %q", code, stderr)
	}
	if code, stdout, stderr := invoke("doctor", "embedded"); code != 0 || !strings.Contains(stdout, "ready") {
		t.Fatalf("doctor must run a module in the engine: %d %q %q", code, stdout, stderr)
	}
}

func TestRunNeedsNoSelectionAndGrantsNothingByDefault(t *testing.T) {
	module := buildGuest(t)
	t.Setenv("HOME", "/leaked-home")
	t.Setenv("FOO", "leaked-foo")
	// An empty selection means the built-in engine; the project chooses nothing.
	code, stdout, stderr := invoke("run", "--", module, "env")
	if code != 0 || stdout != `home="" foo=""` {
		t.Fatalf("host environment reached the module: %d %q %q", code, stdout, stderr)
	}
	code, stdout, _ = invoke("run", "embedded", "--", "--env", "FOO=granted", module, "env")
	if code != 0 || stdout != `home="" foo="granted"` {
		t.Fatalf("granted environment: %d %q", code, stdout)
	}
	if code, _, stderr := invoke("run", "other", "--", module, "env"); code != 1 || !strings.Contains(stderr, "not available") {
		t.Fatalf("unknown engine selection: %d %q", code, stderr)
	}
}

func TestRunExitStatusAndFilesystemGrants(t *testing.T) {
	module := buildGuest(t)
	if code, _, _ := invoke("run", "--", module, "exit", "9"); code != 9 {
		t.Fatalf("exit status was %d", code)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "in.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := invoke("run", "--", module, "read", "/data/in.txt"); code != 3 || stdout != "denied" {
		t.Fatalf("an ungranted path must be invisible: %d %q", code, stdout)
	}
	if code, stdout, _ := invoke("run", "--", "--ro-dir", "/data="+directory, module, "read", "/data/in.txt"); code != 0 || stdout != "read=data" {
		t.Fatalf("granted path: %d %q", code, stdout)
	}
	if code, _, stderr := invoke("run", "--", "--dir", "relative="+directory, module, "env"); code != 2 || !strings.Contains(stderr, "absolute") {
		t.Fatalf("a relative guest path must be refused: %d %q", code, stderr)
	}
}

func TestRunTimeoutAndUsage(t *testing.T) {
	module := buildGuest(t)
	if code, _, stderr := invoke("run", "--", "--timeout", "200ms", module, "spin"); code != 124 || !strings.Contains(stderr, "stopped after") {
		t.Fatalf("a spinning module must be stopped: %d %q", code, stderr)
	}
	if code, _, stderr := invoke("run", "--"); code != 2 || !strings.Contains(stderr, "usage") {
		t.Fatalf("missing module: %d %q", code, stderr)
	}
	if code, _, stderr := invoke("run", "--", filepath.Join(t.TempDir(), "missing.wasm")); code != 1 || stderr == "" {
		t.Fatalf("missing file: %d %q", code, stderr)
	}
	bad := filepath.Join(t.TempDir(), "bad.wasm")
	if err := os.WriteFile(bad, []byte("not wasm"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := invoke("run", "--", bad); code != 1 {
		t.Fatalf("an invalid module must fail: %d", code)
	}
}
