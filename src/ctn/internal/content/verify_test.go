package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeBackend is a test backend that runs content through a shell command.
type fakeBackend struct {
	name   string
	script string
}

func (b *fakeBackend) Name() string { return b.name }

func (b *fakeBackend) Run(ctx context.Context, arguments []string) (int, []byte, error) {
	// Simulate running by checking the script's behavior.
	if b.script == "exit 0" {
		return 0, []byte("output"), nil
	}
	if b.script == "exit 7" {
		return 7, []byte("boom"), nil
	}
	if b.script == "exit 124" {
		return 124, []byte("slow"), nil
	}
	if b.script == "hang" {
		<-ctx.Done()
		return 0, nil, ctx.Err()
	}
	return 1, nil, errors.New("unknown script")
}

// fakePolicy is a test policy that trusts and sandboxes everything.
type fakePolicy struct {
	trusted  bool
	sandboxed bool
}

func (p *fakePolicy) IsTrusted(backend Backend) bool { return p.trusted }

func (p *fakePolicy) Isolation(backend Backend, selection string) (string, string, bool) {
	if p.sandboxed {
		return "sandboxed", "", true
	}
	return "host", "not sandboxed", false
}

func writeContent(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyPasses(t *testing.T) {
	file := writeContent(t, "test.wasm", []byte("content"))
	backend := &fakeBackend{name: "wasm", script: "exit 0"}
	policy := &fakePolicy{trusted: true, sandboxed: true}
	result, err := Verify(context.Background(), file, "", backend, policy, Limits{}, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictPass {
		t.Fatalf("verdict = %v, want PASS", result.Verdict)
	}
	if result.SHA256 == "" {
		t.Fatal("sha256 is empty")
	}
	if result.Backend != "wasm" {
		t.Fatalf("backend = %q, want wasm", result.Backend)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestVerifyFails(t *testing.T) {
	file := writeContent(t, "test.wasm", []byte("content"))
	backend := &fakeBackend{name: "wasm", script: "exit 7"}
	policy := &fakePolicy{trusted: true, sandboxed: true}
	result, err := Verify(context.Background(), file, "", backend, policy, Limits{}, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictFail {
		t.Fatalf("verdict = %v, want FAIL", result.Verdict)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
}

func TestVerifyTimeout(t *testing.T) {
	file := writeContent(t, "test.wasm", []byte("content"))
	backend := &fakeBackend{name: "wasm", script: "hang"}
	policy := &fakePolicy{trusted: true, sandboxed: true}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := Verify(ctx, file, "", backend, policy, Limits{}, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictTimeout {
		t.Fatalf("verdict = %v, want TIMEOUT", result.Verdict)
	}
}

func TestVerifyUntrusted(t *testing.T) {
	file := writeContent(t, "test.wasm", []byte("content"))
	backend := &fakeBackend{name: "wasm", script: "exit 0"}
	policy := &fakePolicy{trusted: false, sandboxed: true}
	result, err := Verify(context.Background(), file, "", backend, policy, Limits{}, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictUnsupported {
		t.Fatalf("verdict = %v, want UNSUPPORTED", result.Verdict)
	}
}

func TestVerifyNotSandboxed(t *testing.T) {
	file := writeContent(t, "test.wasm", []byte("content"))
	backend := &fakeBackend{name: "wasm", script: "exit 0"}
	policy := &fakePolicy{trusted: true, sandboxed: false}
	result, err := Verify(context.Background(), file, "", backend, policy, Limits{}, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictUnsupported {
		t.Fatalf("verdict = %v, want UNSUPPORTED", result.Verdict)
	}
}

func TestVerifyObserver(t *testing.T) {
	file := writeContent(t, "test.wasm", []byte("content"))
	backend := &fakeBackend{name: "wasm", script: "exit 0"}
	policy := &fakePolicy{trusted: true, sandboxed: true}
	var started, completed bool
	observer := &testObserver{
		onStarted: func(s string) { started = true },
		onCompleted: func(r *Result) { completed = true },
	}
	result, err := Verify(context.Background(), file, "", backend, policy, Limits{}, 30*time.Second, observer)
	if err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("observer Started not called")
	}
	if !completed {
		t.Fatal("observer Completed not called")
	}
	if result.Verdict != VerdictPass {
		t.Fatalf("verdict = %v, want PASS", result.Verdict)
	}
}

type testObserver struct {
	onStarted   func(string)
	onCompleted func(*Result)
}

func (o *testObserver) Started(backend string) {
	if o.onStarted != nil {
		o.onStarted(backend)
	}
}

func (o *testObserver) Completed(result *Result) {
	if o.onCompleted != nil {
		o.onCompleted(result)
	}
}
