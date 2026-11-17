package symbols

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSymbolsAreCurrent regenerates the exports with the go:generate line of
// symbols.go and compares them with the files checked in, so a change to the
// engine's exported API cannot leave interpreted guests a stale view of it.
func TestSymbolsAreCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the yaegi extractor")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	source, err := os.ReadFile("symbols.go")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for _, line := range strings.Split(string(source), "\n") {
		if rest, ok := strings.CutPrefix(line, "//go:generate go "); ok {
			args = strings.Fields(rest)
		}
	}
	if len(args) == 0 {
		t.Fatal("symbols.go has no go:generate line")
	}
	// The extractor writes into its working directory and resolves packages
	// from the module, so it runs in a directory inside this package.
	dir, err := os.MkdirTemp(".", ".extract-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command(goTool, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	generated, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(generated) == 0 {
		t.Fatalf("nothing generated: %v", err)
	}
	checkedIn, err := filepath.Glob("github_com-*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(checkedIn) != len(generated) {
		t.Fatalf("%d files generated, %d checked in: run go generate ./internal/symbols", len(generated), len(checkedIn))
	}
	for _, path := range generated {
		want, _ := os.ReadFile(path)
		got, err := os.ReadFile(filepath.Base(path))
		if err != nil || !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
			t.Fatalf("%s is stale: run go generate ./internal/symbols", filepath.Base(path))
		}
	}
}
