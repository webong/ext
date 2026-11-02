package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/ctx/internal/config"
	modpkg "github.com/webong/ext/pkg/ext-go/engine/host/adapter"
	systemgraph "github.com/webong/ext/res/graph"
)

func installKindFixture(t *testing.T, name string, supports string) {
	t.Helper()
	dir := t.TempDir()
	manifest := "api_version = \"2.0\"\nname = \"" + name + "\"\nruntime = \"computer\"\nsurfaces = \"shell\"\nexecutable = \"ctx-" + name + "\"\ncapabilities = \"list,validate,doctor,run\"\nsupports = \"" + supports + "\"\n"
	if err := modpkg.WriteKeyValueManifest(dir, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ctx-"+name), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := adapterStore()
	installed, err := store.Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(installed); err != nil {
		t.Fatal(err)
	}
}

func kindFixtureHome(t *testing.T) (project string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell adapter fixture requires a Unix host")
	}
	home := t.TempDir()
	t.Setenv("EXT_HOME", filepath.Join(home, "ext"))
	t.Setenv("HOME", home)
	project = filepath.Join(home, "work", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	installKindFixture(t, "jvm", "jvm")
	installKindFixture(t, "graal", "jvm")
	installKindFixture(t, "php", "php")
	return project
}

func runDefault(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	resolver, err := newResolver()
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := kindDefaultCommand(resolver, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestKindDefaultCommandProjectAndUser(t *testing.T) {
	project := kindFixtureHome(t)

	if code, out, _ := runDefault(t, "ls"); code != 0 || !strings.Contains(out, "jvm\t-\tnone set; offered by graal, jvm") || strings.Contains(out, "php") {
		t.Fatalf("ls before = %d %q", code, out)
	}
	// Only an installed adapter that supports the kind can be preferred.
	for _, bad := range [][]string{{"jvm", "missing"}, {"jvm", "php"}, {"Bad", "jvm"}, {"jvm"}, {"jvm", "graal", "jvm"}, {}, {"ls", "x"}} {
		if code, _, _ := runDefault(t, bad...); code == 0 {
			t.Fatalf("%v must fail", bad)
		}
	}
	if code, _, errOut := runDefault(t, "jvm", "graal"); code != 0 {
		t.Fatalf("project set: %q", errOut)
	}
	data, err := os.ReadFile(filepath.Join(project, ".ctx"))
	if err != nil || !strings.Contains(string(data), `prefer_jvm = "graal"`) {
		t.Fatalf(".ctx = %q, %v", data, err)
	}
	if code, out, _ := runDefault(t, "ls"); code != 0 || !strings.HasPrefix(out, "jvm\tgraal\t"+filepath.Join(project, ".ctx")) {
		t.Fatalf("ls project = %d %q", code, out)
	}

	// The project preference beats the user one; clearing it exposes the user one.
	if code, _, errOut := runDefault(t, "jvm", "jvm", "--global"); code != 0 {
		t.Fatalf("global set: %q", errOut)
	}
	resolver, _ := newResolver()
	if got, _ := resolver.ResolveKind("jvm"); got.Value != "graal" {
		t.Fatalf("project must win: %+v", got)
	}
	if code, _, errOut := runDefault(t, "jvm", "--clear"); code != 0 {
		t.Fatalf("project clear: %q", errOut)
	}
	resolver, _ = newResolver()
	if got, _ := resolver.ResolveKind("jvm"); got.Value != "jvm" {
		t.Fatalf("user default after clear: %+v", got)
	}
	if code, _, errOut := runDefault(t, "jvm", "--clear", "--global"); code != 0 {
		t.Fatalf("global clear: %q", errOut)
	}
	resolver, _ = newResolver()
	if got, _ := resolver.ResolveKind("jvm"); got.Value != "" {
		t.Fatalf("all cleared: %+v", got)
	}
}

func candidates(adapters ...string) []systemgraph.ContextCandidate {
	var result []systemgraph.ContextCandidate
	for _, adapter := range adapters {
		result = append(result, systemgraph.ContextCandidate{Adapter: adapter, Selection: "s", Supports: []string{"jvm"}})
	}
	return result
}

func TestMarkAndPickKindDefault(t *testing.T) {
	kindFixtureHome(t)
	resolver, _ := newResolver()

	// No preference: nothing is marked, a lone adapter is picked, two are refused.
	marked := candidates("graal", "jvm")
	if err := markKindDefaults(resolver, []string{"jvm"}, marked); err != nil || marked[0].Default || marked[1].Default {
		t.Fatalf("unset mark: %+v %v", marked, err)
	}
	if _, err := pickAdapter(resolver, []string{"jvm"}, candidates("graal", "jvm")); err == nil || !strings.Contains(err.Error(), "2 adapters support jvm (graal, jvm); set one with ctx default jvm <adapter>") {
		t.Fatalf("ambiguous: %v", err)
	}
	if got, err := pickAdapter(resolver, []string{"jvm"}, candidates("graal")); err != nil || len(got) != 1 || got[0].Adapter != "graal" {
		t.Fatalf("lone: %+v %v", got, err)
	}
	if _, err := pickAdapter(resolver, []string{"jvm"}, nil); err == nil || !strings.Contains(err.Error(), "no adapter supports jvm") {
		t.Fatalf("none: %v", err)
	}
	if _, err := pickAdapter(resolver, nil, candidates("jvm")); err == nil {
		t.Fatal("pick without a kind must fail")
	}
	if _, err := pickAdapter(resolver, []string{"jvm", "php"}, candidates("jvm")); err == nil {
		t.Fatal("pick with two kinds must fail")
	}

	if err := config.SetRoot(resolver.ConfigPath, config.KindKey("jvm")+"_default", "graal"); err != nil {
		t.Fatal(err)
	}
	resolver, _ = newResolver()
	marked = candidates("graal", "jvm", "graal")
	if err := markKindDefaults(resolver, []string{"jvm"}, marked); err != nil || !marked[0].Default || marked[1].Default || !marked[2].Default {
		t.Fatalf("mark: %+v %v", marked, err)
	}
	// Marking filters nothing; picking keeps every context of the chosen adapter.
	if got, err := pickAdapter(resolver, []string{"jvm"}, candidates("graal", "jvm", "graal")); err != nil || len(got) != 2 || got[0].Adapter != "graal" || got[1].Adapter != "graal" {
		t.Fatalf("pick: %+v %v", got, err)
	}
	// A preference for an adapter that is gone is an error, not a quiet fallback.
	if _, err := pickAdapter(resolver, []string{"jvm"}, candidates("jvm")); err == nil || !strings.Contains(err.Error(), "the default for jvm is graal") {
		t.Fatalf("stale: %v", err)
	}
}

func TestGraphResolvePickNeedsOneKind(t *testing.T) {
	kindFixtureHome(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"graph", "resolve", "all", "--pick"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "--pick needs exactly one --supports kind") {
		t.Fatalf("pick without a kind = %d %q", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"graph", "resolve", "all", "--supports", "jvm", "--pick"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "no adapter supports jvm") {
		t.Fatalf("pick over no contexts = %d %q", code, errOut.String())
	}
}
