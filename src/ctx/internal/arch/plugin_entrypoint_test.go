package arch_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Go programs reach the plugin system through pkg/ext-go/cgoengine/plugin, which
// re-exports the data model that lives in pkg/ext-go/engine/plugin and runs on
// the C engine or in pure Go. Only the plugin
// library itself and the runtime backends that implement engine.Backend
// (pkg/ext-go/hashicorp, pkg/ext-go/wasm, pkg/ext-go/yaegi, pkg/ext-go/cengine/cguest)
// import that root package. Public provider bridges also use the engine data
// model directly so their consumers can choose the host implementation.
func TestGoProgramsImportCgoengineNotTheEnginePackage(t *testing.T) {
	root := repoRoot(t)
	const pluginRoot = "github.com/webong/ext/pkg/ext-go/engine/plugin"
	for _, tree := range []string{"adapters", "examples", "src", "res"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "node_modules", "testdata", ".git":
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			if tree == "src" {
				rel, _ := filepath.Rel(filepath.Join(root, "src/ctn"), path)
				for _, bridge := range []string{"inference", "model", "perception", "realtime"} {
					if strings.HasPrefix(filepath.ToSlash(rel), bridge+"/") {
						return nil
					}
				}
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
				if name == pluginRoot {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s imports %s; import github.com/webong/ext/pkg/ext-go/cgoengine/plugin instead", filepath.ToSlash(rel), name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
