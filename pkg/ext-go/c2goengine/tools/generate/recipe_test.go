package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCustomRecipeOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "native/src/add.c"), "#include \"add.h\"\nint demo_add(int x) { return x + VALUE + BASE; }\n")
	put(t, filepath.Join(root, "native/include/add.h"), "int demo_add(int x);\n")
	put(t, filepath.Join(root, "native/include/base.h"), "#define BASE 2\n")
	put(t, filepath.Join(root, "binding/api.go"), `//go:build custom_ccgo

package demo
import "modernc.org/libc"
func Add(x int32) int32 { tls:=libc.NewTLS(); defer tls.Close(); return demo_add(tls,x) }
`)
	r := Recipe{Version: 1, SourceRoot: "native", Package: "demo", Sources: []string{"src/add.c"}, IncludeDirs: []string{"include"}, Defines: []string{"VALUE=40"}, ForceIncludes: []string{"include/base.h"}, BuildTag: "custom_ccgo", Bindings: "binding"}
	data, _ := json.Marshal(r)
	recipePath := filepath.Join(root, "recipe.json")
	put(t, recipePath, string(data))
	caller := t.TempDir()
	t.Chdir(caller)
	output, stage := filepath.Join(caller, "output"), filepath.Join(caller, "stage")
	var log bytes.Buffer
	if _, err := Generate(context.Background(), Config{RecipeFile: recipePath, OutputDir: output, StageDir: stage, Log: &log}); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(output, "generated_"+runtime.GOOS+"_"+runtime.GOARCH+".go")
	raw, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("//go:build custom_ccgo &&")) {
		t.Fatal("custom build tag missing")
	}
	// A custom recipe with no output flags defaults beside the recipe, not the caller.
	if _, err := Generate(context.Background(), Config{RecipeFile: recipePath, Log: &log}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "generated", filepath.Base(generated))); err != nil {
		t.Fatal(err)
	}
	// Reuse must not need the C source tree or an Ext checkout.
	if err = os.RemoveAll(filepath.Join(root, "native")); err != nil {
		t.Fatal(err)
	}
	reused := filepath.Join(caller, "reused")
	if _, err = Generate(context.Background(), Config{Translations: stage, StageDir: reused, Log: &log}); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(reused, "go.mod"), "module example.com/custom\n\ngo 1.26.0\n\nrequire modernc.org/libc "+libcVersion+"\n")
	put(t, filepath.Join(reused, "go.sum"), string(compilerSums))
	put(t, filepath.Join(reused, "api_test.go"), `package demo
import "testing"
func TestAdd(t *testing.T){ if got:=Add(0);got!=42{t.Fatalf("Add(0)=%d",got)} }
`)
	cmd := exec.Command("go", "test", "-mod=mod", ".")
	cmd.Dir = reused
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("consume outside workspace: %v\n%s", err, out)
	}
}

func TestExplicitInputsDoNotFallBack(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, config := range []Config{
		{RecipeFile: "missing.json"}, {Translations: "missing", StageDir: "stage"},
		{RecipeFile: "missing.json", Translations: "other"}, {Translations: "other"},
		{OutputFile: "a.go", OutputDir: "other"}, {Library: "custom"},
	} {
		if _, err := Generate(context.Background(), config); err == nil {
			t.Fatalf("expected explicit failure for %+v", config)
		}
	}
	if _, err := Generate(context.Background(), Config{}); err == nil || !strings.Contains(err.Error(), "lib/plugin/CMakeLists.txt") {
		t.Fatalf("default recipe: %v", err)
	}

}

func TestRecipeValidation(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{
		`{"version":2,"sources":["a.c"]}`, `{"version":1,"sources":["../a.c"]}`,
		`{"version":1,"sources":["a.c"],"package":"func"}`,
		`{"version":1,"sources":["a.c"],"build_tag":"x || y"}`,
		`{"version":1,"sources":["a.c"],"typo":true}`, `{"version":1,"sources":[]}`,
	} {
		p := filepath.Join(dir, "recipe.json")
		put(t, p, body)
		if _, err := LoadRecipe(p); err == nil {
			t.Fatalf("accepted recipe %s", body)
		}
	}
}

func TestStagingUsesSelectedTranslation(t *testing.T) {
	root := t.TempDir()
	binding := filepath.Join(root, "binding")
	put(t, filepath.Join(binding, "api.go"), "package ccgo\n")
	suffix := "_" + runtime.GOOS + "_" + runtime.GOARCH + ".go"
	put(t, filepath.Join(binding, "generated"+suffix), "package ccgo\nconst Value=1\n")
	selected := filepath.Join(root, "chosen.go")
	put(t, selected, "package ccgo\nconst Value=2\n")
	stage := filepath.Join(root, "stage")
	if _, err := stageGenerated(binding, selected, stage, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stage, "generated"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("Value=2")) {
		t.Fatal("staged a stale default translation")
	}
	if _, err := Generate(context.Background(), Config{Translations: binding, StageDir: binding}); err == nil {
		t.Fatal("overwrote input directory")
	}
}

func TestReuseSelectsExplicitBuildTags(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	stage := filepath.Join(root, "stage")
	put(t, filepath.Join(source, "selected.go"), "//go:build chosen && extra\n\npackage example\n")
	put(t, filepath.Join(source, "other.go"), "//go:build omitted\n\npackage example\n")
	put(t, filepath.Join(source, "selected_test.go"), "package example\n")
	if _, err := Generate(context.Background(), Config{Translations: source, StageDir: stage, Tags: []string{"chosen", "extra"}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "selected_"+runtime.GOOS+"_"+runtime.GOARCH+".go" {
		t.Fatalf("unexpected staged files: %v", entries)
	}
	data, err := os.ReadFile(filepath.Join(stage, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("//go:build")) {
		t.Fatal("staged file retained its build tag")
	}
	original, err := os.ReadFile(filepath.Join(source, "selected.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(original, []byte("//go:build")) {
		t.Fatal("input file was modified")
	}
}
