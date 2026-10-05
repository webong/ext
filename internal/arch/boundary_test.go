package arch_test

// Repository-wide dependency-direction checks. CTX internals, product adapters,
// commands and examples are implementation packages: reusable libraries must not
// depend back on their consumers.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var forbiddenPrefixes = []string{
	"github.com/webong/ctx/internal/",
	"github.com/webong/ctx/adapters/",
	"github.com/webong/ctx/cmd/",
	"github.com/webong/ctx/examples/",
}

// libraryRoots are the reusable trees that consumers may depend on. Each is
// expected to stay free of implementation imports so it can back its own module.
var libraryRoots = []string{
	"res",
	"pkg/adapter",
	"pkg/graph",
	"pkg/supervisor",
	"pkg/plugin",
	"pkg/go",
}

// repoRoot walks up from the test's directory to the module root, so the check
// works no matter which package `go test` runs from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate module root")
		}
		dir = parent
	}
}

func TestLibraryImportBoundary(t *testing.T) {
	root := repoRoot(t)
	for _, lib := range libraryRoots {
		libPath := filepath.Join(root, lib)
		if _, err := os.Stat(libPath); err != nil {
			t.Errorf("library root %s is missing", lib)
			continue
		}
		err := filepath.WalkDir(libPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				for _, prefix := range forbiddenPrefixes {
					if strings.HasPrefix(name, prefix) {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s imports implementation package %s", rel, name)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
