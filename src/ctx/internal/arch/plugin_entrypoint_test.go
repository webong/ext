package arch_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"testing"
)

// Go programs reach the plugin system through pkg/plugin-go, which re-exports the
// pkg/plugin data model and runs on the C engine or in pure Go. Only the plugin
// libraries themselves import the pkg/plugin root: pkg/plugin, the runtime
// backends that implement plugin.Backend, and the language bindings.
func TestGoProgramsImportPluginGoNotThePluginRoot(t *testing.T) {
	root := repoRoot(t)
	const pluginRoot = "github.com/webong/ext/pkg/plugin"
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
					t.Errorf("%s imports %s; import github.com/webong/ext/pkg/plugin-go instead", filepath.ToSlash(rel), name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
