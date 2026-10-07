package arch_test

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

// lgplModules are dependencies licensed under the LGPL. They may be linked only
// into the one adapter executable that needs them, so the license terms stay
// with that executable and never extend to ctx, ctn or the libraries.
var lgplModules = map[string]string{
	"github.com/ethereum/go-ethereum": "adapters/evm",
}

func TestLGPLDependenciesStayInsideTheirAdapter(t *testing.T) {
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
			case ".git", "node_modules", "target", "dist", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if name != "go.mod" && !strings.HasSuffix(name, ".go") {
			return nil
		}
		for module, home := range lgplModules {
			if relative == home+"/go.mod" || strings.HasPrefix(relative, home+"/") {
				continue
			}
			if name == "go.mod" {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if strings.Contains(string(data), module) {
					t.Errorf("%s requires %s, which is LGPL-licensed and allowed only under %s", relative, module, home)
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
					t.Errorf("%s imports %s, which is LGPL-licensed and allowed only under %s", relative, imported, home)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheLGPLAdapterShipsItsNotice(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"NOTICE.md", "third_party/go-ethereum/COPYING.LESSER", "third_party/go-ethereum/COPYING"} {
		if _, err := os.Stat(filepath.Join(root, "adapters", "evm", name)); err != nil {
			t.Errorf("adapters/evm is missing %s: %v", name, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, "adapters", "evm", "adapter.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "NOTICE.md") || !strings.Contains(string(manifest), "third_party") {
		t.Error("adapter.toml must list NOTICE.md and third_party in package_files so the notice travels with the binary")
	}
}
