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
	"github.com/webong/ext/ctx/",
	"github.com/webong/ext/ctn/",
	"github.com/webong/ext/adapters/",
	"github.com/webong/ext/examples/",
}

// libraryRoots are the reusable trees that consumers may depend on. Each is
// expected to stay free of implementation imports so it can back its own module.
var libraryRoots = []string{
	"res",
	"pkg/graph",
	"pkg/plugin",
	"pkg/plugin-go",
}

// repoRoot walks up from the test's directory to the workspace root, the
// directory holding go.work, so the check works no matter which package
// `go test` runs from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate workspace root")
		}
		dir = parent
	}
}

// productRoots are the binaries under src. Each is its own module and must not
// depend on another product; shared code belongs in pkg or res.
var productRoots = []struct{ dir, module string }{
	{"src/ctx", "github.com/webong/ext/ctx"},
	{"src/ctn", "github.com/webong/ext/ctn"},
}

func TestProductsDoNotImportEachOther(t *testing.T) {
	root := repoRoot(t)
	for _, product := range productRoots {
		var forbidden []string
		for _, other := range productRoots {
			if other != product {
				forbidden = append(forbidden, other.module)
			}
		}
		err := filepath.WalkDir(filepath.Join(root, product.dir), func(path string, entry fs.DirEntry, walkErr error) error {
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
				for _, prefix := range forbidden {
					if name == prefix || strings.HasPrefix(name, prefix+"/") {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s imports another product %s", rel, name)
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
