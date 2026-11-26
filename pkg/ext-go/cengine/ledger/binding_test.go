package ledger_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	ledger "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"github.com/webong/ext/pkg/ext-go/core/ledger/ledgertest"
	process "github.com/webong/ext/pkg/ext-go/core/ledger/observation"
)

func open(t *testing.T) *ledger.Graph {
	t.Helper()
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	n, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Bind(n)
	if err != nil {
		t.Fatal(err)
	}
	s, err := l.OpenStore(context.Background(), "memory", "", ledger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := ledger.NewGraph(context.Background(), s)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	return g
}

func TestNativeProgramScan(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := "ledger-fixture-tool"
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	tool := filepath.Join(dir, file)
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	programs, err := lib.ScanPrograms(context.Background(), []string{"ledger-fixture-tool"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(tool)
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) != 1 || programs[0].Name != "ledger-fixture-tool" ||
		programs[0].Path != tool || programs[0].RealPath != real || !programs[0].OnPath {
		t.Fatalf("programs = %+v", programs)
	}
	inspected, err := lib.InspectProgram(context.Background(), "fixture", tool)
	if err != nil || inspected.Name != "fixture" || inspected.RealPath != real {
		t.Fatalf("inspect program: %+v, %v", inspected, err)
	}
	if _, err := lib.InspectProgram(context.Background(), "fixture", filepath.Join(dir, "missing")); ledger.Code(err) != "not_found" {
		t.Fatalf("inspect missing program: %v", err)
	}
}

func TestNativeHostPath(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	filesystem, err := lib.FilesystemAt(context.Background(), dir)
	if err != nil || filesystem.MountPoint == "" {
		t.Fatalf("filesystem at %s: %+v, %v", dir, filesystem, err)
	}
	if err := lib.CheckHostAccess(context.Background(), dir, "directory_writable"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "tool")
	if err := os.WriteFile(file, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lib.CheckHostAccess(context.Background(), file, "executable_access"); err != nil {
		t.Fatal(err)
	}
	if err := lib.CheckHostAccess(context.Background(), filepath.Join(dir, "missing"), "executable_access"); ledger.Code(err) != "not_found" {
		t.Fatalf("missing executable access: %v", err)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lib.CheckHostAccess(context.Background(), file, "executable_access"); ledger.Code(err) != "denied" {
		t.Fatalf("non-executable access: %v", err)
	}
}

func TestNativeProcessStart(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	start, err := lib.ProcessStart(context.Background(), os.Getpid())
	if err != nil || start == "" {
		t.Fatalf("current process birth token: %q, %v", start, err)
	}
	if _, err := lib.ProcessStart(context.Background(), 0); ledger.Code(err) != "invalid" {
		t.Fatalf("invalid PID: %v", err)
	}
}

func TestNativeServiceRender(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	spec := ledger.ServiceSpec{
		Name: "com.example.agent", Description: "Example agent",
		Executable:       "/usr/local/bin/example-agent",
		Arguments:        []string{"--serve", "with space"},
		Environment:      map[string]string{"EXAMPLE_ROOT": "/var/lib/example", "EXAMPLE_MODE": "strict"},
		WorkingDirectory: "/var/lib/example", Restart: "on-failure", RestartSeconds: 3,
	}
	for _, platform := range []string{"darwin", "linux"} {
		definition, err := lib.RenderService(context.Background(), platform, spec)
		if err != nil {
			t.Fatalf("%s: %v", platform, err)
		}
		if definition.Platform != platform || definition.Content == "" ||
			definition.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte(definition.Content))) {
			t.Fatalf("%s: %+v", platform, definition)
		}
		if platform == "darwin" && !strings.Contains(definition.Content, "<key>Label</key><string>com.example.agent</string>") {
			t.Fatalf("darwin content: %s", definition.Content)
		}
		if platform == "linux" && !strings.Contains(definition.Content, "ExecStart=\"/usr/local/bin/example-agent\" \"--serve\" \"with space\"") {
			t.Fatalf("linux content: %s", definition.Content)
		}
	}
	spec.Name = "Example Agent"
	spec.Executable = `C:\Program Files\agent.exe`
	spec.WorkingDirectory = `C:\state`
	spec.Environment = nil
	spec.UserID = `DOMAIN\user`
	definition, err := lib.RenderService(context.Background(), "windows", spec)
	if err != nil || !strings.Contains(definition.Content, `<URI>\Example Agent</URI>`) {
		t.Fatalf("windows: %+v, %v", definition, err)
	}
}

func TestNativeServiceRenderRejectsInvalidAndEscapes(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	spec := ledger.ServiceSpec{Name: "com.example.agent", Executable: "/usr/bin/true",
		Arguments: []string{"%h", "$HOME", `back\slash`, `quote"d`}}
	definition, err := lib.RenderService(ctx, "linux", spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, escaped := range []string{"%%h", "$$HOME", `back\\slash`, `\"`} {
		if !strings.Contains(definition.Content, escaped) {
			t.Errorf("missing escaped %q in %s", escaped, definition.Content)
		}
	}
	invalid := []ledger.ServiceSpec{
		{Name: "../escape", Executable: "/usr/bin/true"},
		{Name: "com.example.agent", Executable: "relative"},
		{Name: "com.example.agent", Executable: "/usr/bin/true", Arguments: []string{"line\nbreak"}},
		{Name: "com.example.agent", Executable: "/usr/bin/true", Restart: "sometimes"},
	}
	for _, item := range invalid {
		if _, err := lib.RenderService(ctx, "linux", item); ledger.Code(err) != "invalid" {
			t.Errorf("spec %+v: %v", item, err)
		}
	}
	if _, err := lib.RenderService(ctx, "windows", ledger.ServiceSpec{
		Name: "Example Agent", Executable: `C:\agent.exe`, UserID: `DOMAIN\user`,
		Environment: map[string]string{"TOKEN": "x"},
	}); ledger.Code(err) != "unsupported" {
		t.Fatalf("windows environment: %v", err)
	}
}

func TestNativeServicePublication(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	executable := "/usr/bin/true"
	if runtime.GOOS == "windows" {
		executable = `C:\Windows\System32\cmd.exe`
	}
	definition, err := lib.RenderService(context.Background(), runtime.GOOS, ledger.ServiceSpec{
		Name: "com.example.ledger", Executable: executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	initial, err := lib.InspectService(ctx, definition, dir)
	if err != nil || initial.Installed {
		t.Fatalf("initial inspection: %+v, %v", initial, err)
	}
	published, err := lib.PublishService(ctx, definition, dir)
	if err != nil || !published.Created || !published.Installed || !published.Matches {
		t.Fatalf("publication: %+v, %v", published, err)
	}
	again, err := lib.PublishService(ctx, definition, dir)
	if err != nil || !again.Existing || again.Created {
		t.Fatalf("repeat publication: %+v, %v", again, err)
	}
	changed := definition
	changed.Content += "\n"
	changed.Digest = fmt.Sprintf("%x", sha256.Sum256([]byte(changed.Content)))
	inspection, err := lib.InspectService(ctx, changed, dir)
	if err != nil || !inspection.Installed || inspection.Matches || inspection.Digest != definition.Digest {
		t.Fatalf("changed inspection: %+v, %v", inspection, err)
	}
	if _, err := lib.PublishService(ctx, changed, dir); ledger.Code(err) != "exists" {
		t.Fatalf("publish changed definition: %v", err)
	}
	if _, err := lib.RemoveService(ctx, changed, dir); ledger.Code(err) != "changed" {
		t.Fatalf("remove changed definition: %v", err)
	}
	removed, err := lib.RemoveService(ctx, definition, dir)
	if err != nil || removed.Installed {
		t.Fatalf("removal: %+v, %v", removed, err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.PublishService(ctx, definition, link); ledger.Code(err) != "denied" {
			t.Fatalf("symlink directory: %v", err)
		}
	}
}
func TestNativeRecorderAndInventory(t *testing.T) {
	g := open(t)
	ctx := context.Background()
	if err := g.ObserveShell(ctx, t.TempDir(), "fixture", map[string]string{"browser": "work"}); err != nil {
		t.Fatal(err)
	}
	if err := g.ObserveInventory(ctx, []ledger.AdapterObservation{{Name: "fixture", Runtime: "browser", Trusted: true, Listed: true, ListSupported: true, DiscoveryStatus: "ok", Capabilities: []string{"open"}, Supports: []string{"web"}, Contexts: []ledger.ContextObservation{{Selection: "work"}}, Resources: []ledger.ResourceObservation{{ID: "one", Kind: "page", Context: "work"}}}}, nil); err != nil {
		t.Fatal(err)
	}
	inv, err := g.ResolveInventory(ctx, "browser")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Contexts) != 1 || inv.Contexts[0].Selection != "work" || inv.Contexts[0].Adapter != "fixture" {
		t.Fatalf("inventory: %+v", inv)
	}
	snap, err := g.Store.Snapshot(ctx, ledger.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	var shell, resource bool
	for _, v := range snap.Vertices {
		shell = shell || v.Kind == "ext.system/shell-session"
		resource = resource || v.Kind == "ext.system/resource"
	}
	if !shell || !resource {
		t.Fatalf("missing C recorder facts: %+v", snap)
	}
	if err := g.ObserveInventory(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	inv, err = g.ResolveInventory(ctx, "browser")
	if err != nil || len(inv.Contexts) != 0 {
		t.Fatalf("stale inventory: %+v %v", inv, err)
	}
}
func TestNativeProcessAndCancellation(t *testing.T) {
	g := open(t)
	if code := ledger.Code(g.Store.Register(context.Background(), ledger.Schema{Namespace: "bad namespace", Version: "1"})); code != "invalid" {
		t.Fatalf("native error code: %s", code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.ObserveShell(ctx, t.TempDir(), "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	snap, err := g.Store.Snapshot(context.Background(), ledger.Namespace)
	if err != nil || len(snap.Vertices) != 0 {
		t.Fatalf("canceled call wrote graph: %v %v", snap, err)
	}
	observed, err := g.InspectProcess(context.Background(), os.Getpid(), process.ProcessOptions{MaxResources: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Processes) != 1 || observed.Processes[0].PID != os.Getpid() {
		t.Fatalf("wrong process: %+v", observed)
	}
}

func TestNativeClose(t *testing.T) {
	g := open(t)
	if code := ledger.Code(g.Store.Close()); code != "busy" {
		t.Fatalf("store with live recorder: %s", code)
	}
	for i := 0; i < 2; i++ {
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Store.Snapshot(context.Background(), ledger.Namespace); !errors.Is(err, ledger.ErrClosed) {
		t.Fatalf("closed store: %v", err)
	}
}

func TestNativeStoreConformance(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	n, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Bind(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			b := ledgertest.Backend{Durable: backend == "file", Open: func(t testing.TB, dir string, o ledger.Options) ledger.Store {
				s, err := l.OpenStore(context.Background(), backend, dir, o)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}}
			if backend == "file" {
				ledgertest.Run(t, b)
			}
			ledgertest.RunBehaviour(t, b)
		})
	}
}

func TestNativeShellSearchPath(t *testing.T) {
	g := open(t)
	ctx := context.Background()
	dir := t.TempDir()
	name := "sh"
	if runtime.GOOS == "windows" {
		name = "cmd.exe"
	}
	candidate := filepath.Join(dir, name)
	if err := os.WriteFile(candidate, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	before := os.Getenv("PATH")
	selected, err := g.ObserveRequirements(ctx, ledger.OperationRequirements{Operation: "test.shell", Shell: &ledger.ShellRequirement{SearchPath: &dir}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range selected.Shells {
		found = found || s.Path == candidate
	}
	if !found {
		t.Fatalf("explicit C search path omitted %s: %+v", candidate, selected.Shells)
	}
	empty := ""
	selected, err = g.ObserveRequirements(ctx, ledger.OperationRequirements{Operation: "test.shell", Shell: &ledger.ShellRequirement{SearchPath: &empty}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range selected.Shells {
		if s.Path == candidate {
			t.Fatal("empty search path retained prior PATH candidate")
		}
	}
	if os.Getenv("PATH") != before {
		t.Fatal("collector mutated process PATH")
	}
}

func TestNativeTransactionFingerprints(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	ledgertest.RunFingerprints(t, func(tx ledger.Transaction) (json.RawMessage, string, error) {
		return lib.CanonicalTransaction(context.Background(), tx)
	})
}

func TestNativeAttributeValidation(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		attributes map[string]any
		code       string
	}{
		{"empty", nil, ""},
		{"ordinary", map[string]any{"label": "public"}, ""},
		{"secret-key", map[string]any{"password": "hidden"}, "denied"},
		{"nested-secret", map[string]any{"nested": []any{map[string]any{"label": "secret-value:hidden"}}}, "denied"},
		{"bounded", map[string]any{"label": strings.Repeat("x", 4097)}, "too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := lib.ValidateAttributes(context.Background(), test.attributes); ledger.Code(err) != test.code {
				t.Fatalf("validation: %v; want %s", err, test.code)
			}
		})
	}
}
