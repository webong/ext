package generate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/webong/ext/pkg/ext-go/c2goengine/tools/generate"
)

func TestInlineRecipesDoNotChangeCallerState(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const count = 2
	configs := make([]generate.Config, count)
	for i := range configs {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "native"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "native/answer.c"), []byte("int answer(void) { return 42; }\n"), 0644); err != nil {
			t.Fatal(err)
		}
		configs[i] = generate.Config{BaseDir: dir, Recipe: &generate.Recipe{SourceRoot: "native", Package: "answer", Sources: []string{"answer.c"}}, OutputDir: "output", StageDir: "staged"}
	}
	type outcome struct {
		result generate.Result
		err    error
	}
	results := make([]outcome, count)
	var workers sync.WaitGroup
	for i := range configs {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			results[i].result, results[i].err = generate.Generate(context.Background(), configs[i])
		}(i)
	}
	workers.Wait()
	for i, out := range results {
		if out.err != nil {
			t.Fatal(out.err)
		}
		if !filepath.IsAbs(out.result.OutputFile) || filepath.Dir(out.result.OutputFile) != filepath.Join(configs[i].BaseDir, "output") {
			t.Fatalf("wrong output: %+v", out.result)
		}
		if out.result.StageDir != filepath.Join(configs[i].BaseDir, "staged") || out.result.StagedFiles != 1 {
			t.Fatalf("wrong staging result: %+v", out.result)
		}
		if _, err := os.Stat(out.result.OutputFile); err != nil {
			t.Fatal(err)
		}
		if configs[i].Recipe.Version != 0 || configs[i].Recipe.SourceRoot != "native" || !reflect.DeepEqual(configs[i].Recipe.Sources, []string{"answer.c"}) {
			t.Fatal("caller recipe mutated")
		}
	}
	after, err := os.Getwd()
	if err != nil || after != cwd {
		t.Fatalf("caller cwd changed: %q -> %q (%v)", cwd, after, err)
	}
}

func TestCanceledGenerationDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := generate.Generate(ctx, generate.Config{BaseDir: dir, Recipe: &generate.Recipe{Sources: []string{"absent.c"}}, OutputDir: "output"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "output")); !os.IsNotExist(err) {
		t.Fatalf("canceled generation wrote output: %v", err)
	}
}
