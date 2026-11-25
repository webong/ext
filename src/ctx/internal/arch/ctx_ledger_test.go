package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// CTX graph behavior stays on the C binding; graph records come from core.
// No Go SQLite implementation or legacy upgrade reader belongs here.
func TestCTXGraphUsesNativeLedger(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"src/ctx/internal/app", "src/ctx/internal/graphapi"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, dir, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			names := map[string]string{}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasPrefix(name, "github.com/webong/ext/pkg/ext-go/engine/ledger") || name == "database/sql" || name == "modernc.org/sqlite" {
					t.Errorf("%s imports legacy recorder %s", path, name)
				}
				alias := filepath.Base(name)
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				names[alias] = name
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				pkg := names[id.Name]
				if pkg == "github.com/webong/ext/pkg/ext-go/engine/ledger" && sel.Sel.Name == "NewMemory" {
					t.Errorf("%s opens the compatibility Go store", path)
				}
				if strings.HasPrefix(entry.Name(), "graph") && strings.HasPrefix(pkg, "github.com/webong/ext/res/system/") && strings.HasPrefix(sel.Sel.Name, "Discover") {
					t.Errorf("%s collects graph inventory outside the C ledger", path)
				}
				return true
			})
		}
	}
}
