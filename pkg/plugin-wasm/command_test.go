package wasm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var commandModule []byte

// buildCommandModule compiles the WASI test guest once per test binary run.
func buildCommandModule(t *testing.T) []byte {
	t.Helper()
	if commandModule != nil {
		return commandModule
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain is required to build the WASI test guest")
	}
	output := filepath.Join(t.TempDir(), "command.wasm")
	build := exec.Command(goTool, "build", "-o", output, "./testdata/command")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if message, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build WASI guest: %v\n%s", err, message)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	commandModule = data
	return data
}

func run(t *testing.T, module []byte, opts CommandOptions) (code int, stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	opts.Stdout, opts.Stderr = &out, &errOut
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err = RunCommand(ctx, module, opts)
	return code, out.String(), errOut.String(), err
}

func TestRunCommandArgumentsAndEnvironment(t *testing.T) {
	module := buildCommandModule(t)
	t.Setenv("FOO", "leaked")
	t.Setenv("HOME", "/leaked")
	code, stdout, _, err := run(t, module, CommandOptions{Name: "tool", Args: []string{"echo", "a", "b c"}, Env: map[string]string{"FOO": "granted"}})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if want := `argv0=tool args=a,b c foo="granted" home=""`; !strings.Contains(stdout, want) {
		t.Fatalf("stdout %q does not contain %q (the host environment must not be inherited)", stdout, want)
	}
}

func TestRunCommandStdinAndExitStatus(t *testing.T) {
	module := buildCommandModule(t)
	_, stdout, _, err := run(t, module, CommandOptions{Args: []string{"upper"}, Stdin: strings.NewReader("one\ntwo\n")})
	if err != nil || stdout != "ONE\nTWO\n" {
		t.Fatalf("stdout %q err=%v", stdout, err)
	}
	// With no stdin granted the module sees end of input.
	if _, stdout, _, err = run(t, module, CommandOptions{Args: []string{"upper"}}); err != nil || stdout != "" {
		t.Fatalf("ungranted stdin: %q %v", stdout, err)
	}
	code, _, stderr, err := run(t, module, CommandOptions{Args: []string{"exit", "7"}})
	if err != nil || code != 7 || !strings.Contains(stderr, "exiting") {
		t.Fatalf("exit status: code=%d stderr=%q err=%v", code, stderr, err)
	}
}

func TestRunCommandFilesystemIsOptInAndConfined(t *testing.T) {
	module := buildCommandModule(t)
	shared, secret := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(shared, "in.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "key.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No mount: nothing on the host is visible.
	code, stdout, _, _ := run(t, module, CommandOptions{Args: []string{"read", "/data/in.txt"}})
	if code != 3 || strings.Contains(stdout, "hello") {
		t.Fatalf("unmounted read: code=%d stdout=%q", code, stdout)
	}
	// A granted directory is readable and writable.
	opts := CommandOptions{Args: []string{"read", "/data/in.txt"}, Dirs: map[string]string{"/data": shared}}
	if code, stdout, _, err := run(t, module, opts); err != nil || code != 0 || stdout != "read=hello" {
		t.Fatalf("mounted read: code=%d stdout=%q err=%v", code, stdout, err)
	}
	opts.Args = []string{"write", "/data/out.txt", "written"}
	if code, _, _, err := run(t, module, opts); err != nil || code != 0 {
		t.Fatalf("mounted write: code=%d err=%v", code, err)
	}
	if data, err := os.ReadFile(filepath.Join(shared, "out.txt")); err != nil || string(data) != "written" {
		t.Fatalf("host file %q %v", data, err)
	}
	// A read-only mount rejects writes.
	readonly := CommandOptions{Args: []string{"write", "/ro/new.txt", "x"}, ReadOnlyDirs: map[string]string{"/ro": shared}}
	if code, _, _, _ := run(t, module, readonly); code != 3 {
		t.Fatalf("write to a read-only mount exited %d", code)
	}
	if _, err := os.Stat(filepath.Join(shared, "new.txt")); err == nil {
		t.Fatal("a read-only mount was written")
	}
	// The module cannot reach outside the granted directory.
	escape := CommandOptions{Args: []string{"read", "/data/../" + filepath.Base(secret) + "/key.txt"}, Dirs: map[string]string{"/data": shared}}
	if _, stdout, _, _ := run(t, module, escape); strings.Contains(stdout, "secret") {
		t.Fatalf("escaped the mount: %q", stdout)
	}
}

func TestRunCommandCancelsAComputeLoop(t *testing.T) {
	module := buildCommandModule(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := RunCommand(ctx, module, CommandOptions{Args: []string{"spin"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a spinning module must be cancelled: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestRunCommandRejectsBadModules(t *testing.T) {
	for _, module := range [][]byte{nil, []byte("not wasm")} {
		if _, err := RunCommand(context.Background(), module, CommandOptions{}); err == nil {
			t.Fatalf("module %q was accepted", module)
		}
	}
}
