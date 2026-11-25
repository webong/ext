package arch_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Backend root packages consume core definitions. The remaining engine imports
// are explicit implementations or the published installed-package convenience API.
// Guest symbol exports, example commands and test oracles retain their old paths.
func TestBackendLibrariesUseSharedContracts(t *testing.T) {
	allowed := map[string]map[string]bool{
		"yaegi/package.go": {pluginPrefix + "/engine/plugin": true, pluginPrefix + "/engine/plugin/host/store": true, pluginPrefix + "/engine/plugin/package": true},
		"yaegi/yaegi.go":   {pluginPrefix + "/engine/plugin/transport/inprocess": true},
		"wasm/wasm.go":     {pluginPrefix + "/engine/plugin/transport/jsonline": true},
	}
	for _, backend := range []string{"yaegi", "wasm", "hashicorp"} {
		dir := filepath.Join(repoRoot(t), "pkg/ext-go", backend)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasPrefix(name, pluginPrefix+"/engine/") && !allowed[backend+"/"+entry.Name()][name] {
					t.Errorf("%s/%s imports implementation %s for a shared contract", backend, entry.Name(), name)
				}
			}
		}
	}
}
