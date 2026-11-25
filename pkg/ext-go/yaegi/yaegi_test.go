package yaegi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/traefik/yaegi/interp"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest"
	"github.com/webong/ext/pkg/ext-go/yaegi"
)

// guestDir copies a guest's files into a new directory and returns it with
// its digest, as a tree artifact records it.
func guestDir(t testing.TB, files map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := plugin.DirectoryDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, digest
}

func conformanceSource(t testing.TB) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "conformance", "guest.go"))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"guest.go": string(data)}
}

// The interpreted reference guest is held to the same conformance suite as
// the compiled backends.
func TestConformance(t *testing.T) {
	dir, digest := guestDir(t, conformanceSource(t))
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		return yaegi.Open(ctx, dir, digest, yaegi.Options{})
	})
}

func open(t *testing.T, files map[string]string, options yaegi.Options) (*yaegi.Backend, error) {
	t.Helper()
	dir, digest := guestDir(t, files)
	b, err := yaegi.Open(context.Background(), dir, digest, options)
	if b != nil {
		t.Cleanup(func() { _ = b.Close() })
	}
	return b, err
}

const header = "package main\n\nimport (\n\t\"context\"\n\tplugin \"github.com/webong/ext/pkg/ext-go/engine/plugin\"\n\t\"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/inprocess\"\n%s)\n\nvar _ = inprocess.New\n\n"

func guestWith(imports, body string) map[string]string {
	return map[string]string{"guest.go": strings.Replace(header, "%s", imports, 1) + body}
}

const factory = `
func ExtPlugin(ctx context.Context) (plugin.Backend, error) {
	d := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "test/guest", Revision: "1"},
		Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "test", Version: "v1"}, Operations: []plugin.Operation{{Name: "run"}}}}}
	g, err := plugin.NewGuest(d, plugin.GuestOptions{Handler: handle})
	if err != nil {
		return nil, err
	}
	b, err := inprocess.New(g)
	if err != nil {
		return nil, err
	}
	return b, nil
}
`

// A guest that imports a package outside the allow-list is refused before any
// of it is evaluated, whichever forbidden package it names.
func TestDisallowedImportIsRefused(t *testing.T) {
	for _, pkg := range []string{"os/exec", "unsafe", "syscall", "reflect", "plugin", "os", "net/http", "runtime", "C", "github.com/traefik/yaegi/interp", "github.com/webong/ext/pkg/ext-go/yaegi/internal/park", "example.com/elsewhere"} {
		t.Run(pkg, func(t *testing.T) {
			files := guestWith("\t_ \""+pkg+"\"\n", factory+"\nfunc handle(_ context.Context, _ plugin.Request) ([]byte, error) { return nil, nil }\n")
			_, err := open(t, files, yaegi.Options{})
			if !errors.Is(err, plugin.ErrDenied) || !strings.Contains(err.Error(), pkg) {
				t.Fatalf("import %s: %v", pkg, err)
			}
		})
	}
	// The import is refused even from a second file of the package.
	files := conformanceSource(t)
	files["extra.go"] = "package main\n\nimport _ \"os/exec\"\n"
	if _, err := open(t, files, yaegi.Options{}); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("second file: %v", err)
	}
}

// The host grants a package by listing it, and an empty list grants none.
func TestHostChoosesPackages(t *testing.T) {
	body := factory + `
func handle(_ context.Context, r plugin.Request) (json.RawMessage, error) {
	return json.Marshal(strings.ToUpper(string(r.Payload)))
}
`
	files := guestWith("\t\"encoding/json\"\n\t\"strings\"\n", body)
	if _, err := open(t, files, yaegi.Options{Packages: []string{"context", "encoding/json"}}); !errors.Is(err, plugin.ErrDenied) || !strings.Contains(err.Error(), `"strings"`) {
		t.Fatalf("unlisted strings: %v", err)
	}
	if _, err := open(t, files, yaegi.Options{Packages: []string{}}); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("empty list: %v", err)
	}
	if _, err := open(t, files, yaegi.Options{Packages: []string{"no/such"}}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("unknown package: %v", err)
	}
	b, err := open(t, files, yaegi.Options{Packages: []string{"context", "encoding/json", "strings"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := call(t, b, "run", `"abc"`); got != `"\"ABC\""` {
		t.Fatalf("got %s", got)
	}
	// "os" is the host's explicit grant: with it, the guest can read the
	// process's environment.
	osBody := factory + `
func handle(_ context.Context, _ plugin.Request) (json.RawMessage, error) { return json.Marshal(os.Getpid() > 0) }
`
	b, err = open(t, guestWith("\t\"encoding/json\"\n\t\"os\"\n", osBody), yaegi.Options{Packages: append([]string{"os"}, yaegi.DefaultPackages...)})
	if err != nil {
		t.Fatal(err)
	}
	if got := call(t, b, "run", "null"); got != "true" {
		t.Fatalf("got %s", got)
	}
}

// Host symbols become importable, so a host can hand a guest an API of its own.
func TestHostSymbols(t *testing.T) {
	body := factory + `
func handle(_ context.Context, _ plugin.Request) (json.RawMessage, error) { return json.Marshal(host.Greeting("ext")) }
`
	files := guestWith("\t\"encoding/json\"\n\t\"example.com/host\"\n", body)
	symbols := interp.Exports{"example.com/host/host": {"Greeting": reflectValue(func(name string) string { return "hello " + name })}}
	b, err := open(t, files, yaegi.Options{Symbols: symbols})
	if err != nil {
		t.Fatal(err)
	}
	if got := call(t, b, "run", "null"); got != `"hello ext"` {
		t.Fatalf("got %s", got)
	}
}

// A guest that assigns a package variable changes its own copy, never the
// host's.
func TestPackageVariablesAreCopied(t *testing.T) {
	saved := plugin.ErrDenied
	body := factory + `
func handle(_ context.Context, _ plugin.Request) (json.RawMessage, error) {
	io.EOF = nil
	plugin.ErrDenied = nil
	return json.Marshal(io.EOF == nil && plugin.ErrDenied == nil)
}
`
	b, err := open(t, guestWith("\t\"encoding/json\"\n\t\"io\"\n", body), yaegi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := call(t, b, "run", "null"); got != "true" {
		t.Fatalf("guest saw %s", got)
	}
	if plugin.ErrDenied != saved || plugin.ErrDenied == nil || ioEOF() == nil {
		t.Fatal("a guest changed a host package variable")
	}
}

// The source is held to its digest, and what is evaluated is what was read.
func TestDigestIsChecked(t *testing.T) {
	dir, digest := guestDir(t, conformanceSource(t))
	if _, err := yaegi.Open(context.Background(), dir, strings.Repeat("0", 64), yaegi.Options{}); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("wrong digest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("added"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := yaegi.Open(context.Background(), dir, digest, yaegi.Options{}); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("added file: %v", err)
	}
	if _, err := yaegi.Open(context.Background(), "relative", digest, yaegi.Options{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("relative dir: %v", err)
	}
	if _, err := yaegi.Open(context.Background(), dir, digest, yaegi.Options{MaxSourceFiles: 1}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("file bound: %v", err)
	}
}

// A guest without the factory, or with the wrong signature, is unsupported.
func TestFactoryIsRequired(t *testing.T) {
	if _, err := open(t, guestWith("", "func Other() {}\nvar _ = context.Background\nvar _ plugin.Backend\n"), yaegi.Options{}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("missing factory: %v", err)
	}
	if _, err := open(t, guestWith("", "func ExtPlugin() {}\nvar _ = context.Background\nvar _ plugin.Backend\n"), yaegi.Options{}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("wrong signature: %v", err)
	}
	if _, err := open(t, guestWith("", "func ExtPlugin(ctx context.Context) (plugin.Backend, error) { return nil, nil }\n"), yaegi.Options{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("nil backend: %v", err)
	}
	if _, err := open(t, guestWith("", "func ExtPlugin(ctx context.Context) (plugin.Backend, error) { return nil, nil\n"), yaegi.Options{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("syntax error: %v", err)
	}
}

// Options.Timeout bounds a call without an earlier deadline. A guest that
// loops without returning is stopped, and the backend fails every later call.
func TestTimeoutStopsTheInterpreter(t *testing.T) {
	body := factory + `
func handle(_ context.Context, r plugin.Request) (json.RawMessage, error) {
	if string(r.Payload) == "\"spin\"" {
		n := 0
		for {
			n++
		}
	}
	return r.Payload, nil
}
`
	var stderr bytes.Buffer
	b, err := open(t, guestWith("\t\"encoding/json\"\n", body), yaegi.Options{Timeout: 50 * time.Millisecond, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if got := call(t, b, "run", `"ok"`); got != `"ok"` {
		t.Fatalf("got %s", got)
	}
	spin, later := request(t, b, "run", `"spin"`), request(t, b, "run", `"ok"`)
	before := runtime.NumGoroutine()
	start := time.Now()
	_, err = b.Invoke(context.Background(), spin)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("spin: %v after %v", err, time.Since(start))
	}
	if _, err := b.Invoke(context.Background(), later); !errors.Is(err, yaegi.ErrStopped) {
		t.Fatalf("after stop: %v", err)
	}
	if _, err := b.Handshake(context.Background()); !errors.Is(err, yaegi.ErrStopped) {
		t.Fatalf("handshake after stop: %v", err)
	}
	// The spinning loop has stopped, and so has the supervisor: one goroutine
	// fewer than before the call, not one more.
	for deadline := time.Now().Add(2 * time.Second); runtime.NumGoroutine() >= before; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines, %d before the call", runtime.NumGoroutine(), before)
		}
	}
}

// A panic in the guest fails the call and stops the interpreter; it never
// reaches the host's goroutine.
func TestPanicStopsTheInterpreter(t *testing.T) {
	body := factory + `
func handle(_ context.Context, _ plugin.Request) (json.RawMessage, error) { panic("guest bug") }
`
	var stderr bytes.Buffer
	b, err := open(t, guestWith("\t\"encoding/json\"\n", body), yaegi.Options{Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Invoke(context.Background(), request(t, b, "run", "null")); !errors.Is(err, yaegi.ErrStopped) {
		t.Fatalf("panic: %v", err)
	}
	if !strings.Contains(stderr.String(), "guest bug") {
		t.Fatalf("stderr: %q", stderr.String())
	}
	var _ json.RawMessage
}

// What the guest prints goes to the host's writers, not the process's.
func TestOutputIsRedirected(t *testing.T) {
	body := factory + `
func handle(_ context.Context, _ plugin.Request) (json.RawMessage, error) { fmt.Println("from the guest"); return nil, nil }
`
	var stdout bytes.Buffer
	b, err := open(t, guestWith("\t\"encoding/json\"\n\t\"fmt\"\n", body+"\nvar _ json.RawMessage\n"), yaegi.Options{Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}
	call(t, b, "run", "null")
	if stdout.String() != "from the guest\n" {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func request(t *testing.T, b *yaegi.Backend, operation, payload string) plugin.Request {
	t.Helper()
	d, err := b.Handshake(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plugin.Request{APIVersion: plugin.APIVersion, ID: "1", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef, Operation: operation, Payload: json.RawMessage(payload), Deadline: time.Now().Add(5 * time.Second)}
}

func call(t *testing.T, b *yaegi.Backend, operation, payload string) string {
	t.Helper()
	response, err := b.Invoke(context.Background(), request(t, b, operation, payload))
	if err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("remote error: %+v", response.Error)
	}
	return string(response.Payload)
}
