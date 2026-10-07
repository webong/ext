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

func TestPluginAdapterProtocolDoesNotImportConsumerTrees(t *testing.T) {
	root := repoRoot(t)
	prefixes := []string{"github.com/webong/ext/ctx/internal/", "github.com/webong/ext/adapters/"}
	err := filepath.WalkDir(filepath.Join(root, "pkg/plugin"), func(path string, entry fs.DirEntry, walkErr error) error {
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
			for _, prefix := range prefixes {
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
