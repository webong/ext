package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The legacy session is retained as a fixture oracle. Production entry points
// must not silently regain a second admission, routing or lifecycle engine.
func TestProductionHostsUseCLibraries(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"cmd", "src", "adapters", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "testdata", "plugintest", "bench", "vendor", "node_modules":
					return filepath.SkipDir
				}
				if strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			aliases := map[string]bool{}
			for _, imp := range f.Imports {
				value, _ := strconv.Unquote(imp.Path.Value)
				if value == "github.com/webong/ext/pkg/ext-go/engine/plugin/host" {
					t.Errorf("%s imports the retired Go package host", path)
				}
				if value == "github.com/webong/ext/pkg/ext-go/engine/plugin" {
					name := "plugin"
					if imp.Name != nil {
						name = imp.Name.Name
					}
					aliases[name] = true
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Open" {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if ok && aliases[id.Name] {
					t.Errorf("%s opens the legacy Go session; use the C host", path)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
