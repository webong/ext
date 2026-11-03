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

const webHostModule = "github.com/webong/ext/res/web/host"

// res/web is a container: the Go module is res/web/host, and the module of the
// path github.com/webong/ext/res/web is never tagged again. The group
// directories under host hold packages, not Go files of their own.
func TestWebHostModulePath(t *testing.T) {
	root := repoRoot(t)
	web := filepath.Join(root, "res", "web")
	if _, err := os.Stat(filepath.Join(web, "go.mod")); err == nil {
		t.Fatal("res/web is a container and has no go.mod; the Go module is res/web/host")
	}
	entries, err := os.ReadDir(web)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".go") {
			t.Errorf("res/web/%s: a container holds no Go code", entry.Name())
		}
	}
	data, err := os.ReadFile(filepath.Join(web, "host", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var module string
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			module = strings.TrimSpace(rest)
			break
		}
	}
	if module != webHostModule {
		t.Errorf("res/web/host/go.mod declares module %q, want %q", module, webHostModule)
	}
	for _, group := range []string{"manage", "artifact", "engine"} {
		files, err := os.ReadDir(filepath.Join(web, "host", group))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".go") {
				t.Errorf("res/web/host/%s/%s: a group directory holds packages, not Go files", group, file.Name())
			}
		}
	}
}

// webHostMayImport lists, for each group of the host module, the other groups its
// packages may import. contract is the shared vocabulary and imports nothing of
// the module; artifacts know only the contract; engines stand alone; manage
// combines the contract and the artifacts. examples and internal are consumers
// and are not checked.
var webHostMayImport = map[string][]string{
	"contract": {},
	"artifact": {"contract"},
	"engine":   {},
	"manage":   {"contract", "artifact"},
}

func TestWebHostLayering(t *testing.T) {
	root := repoRoot(t)
	host := filepath.Join(root, "res", "web", "host")
	for group, allowed := range webHostMayImport {
		okay := map[string]bool{group: true}
		for _, a := range allowed {
			okay[a] = true
		}
		err := filepath.WalkDir(filepath.Join(host, group), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return walkErr
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				rest, ok := strings.CutPrefix(name, webHostModule+"/")
				if !ok {
					continue
				}
				if first, _, _ := strings.Cut(rest, "/"); !okay[first] && first != "internal" {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s imports %s; %s may import only %v", filepath.ToSlash(rel), name, group, allowed)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
