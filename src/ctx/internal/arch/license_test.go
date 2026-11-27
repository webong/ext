package arch_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// lgplModules are dependencies licensed under the LGPL. Nothing in ext may link
// them: their only user is the evm adapter in xallet/zip, which ships the
// notice with its own executable, so the license terms never extend to ctx,
// ctn or the libraries.
var lgplModules = []string{
	"github.com/ethereum/go-ethereum",
}

func TestNoLGPLDependencies(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".claude", "node_modules", "target", "dist", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if name != "go.mod" && !strings.HasSuffix(name, ".go") {
			return nil
		}
		for _, module := range lgplModules {
			if name == "go.mod" {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if strings.Contains(string(data), module) {
					t.Errorf("%s requires %s, which is LGPL-licensed and not allowed in ext", relative, module)
				}
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				continue
			}
			for _, spec := range file.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err == nil && (imported == module || strings.HasPrefix(imported, module+"/")) {
					t.Errorf("%s imports %s, which is LGPL-licensed and not allowed in ext", relative, imported)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// licensedPackages are the language packages a registry publishes from their own
// directory: cargo package, npm pack and a Python build read only that directory,
// so each one carries a copy of the root LICENSE.
var licensedPackages = []string{"pkg/ext-rust", "pkg/ext-ts", "pkg/ext-python"}

func TestPackageLicensesMatchTheRoot(t *testing.T) {
	root := repoRoot(t)
	want, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range licensedPackages {
		got, err := os.ReadFile(filepath.Join(root, dir, "LICENSE"))
		if err != nil {
			t.Errorf("%s has no LICENSE: %v", dir, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s/LICENSE differs from the root LICENSE; copy the root file over it", dir)
		}
	}
}
