package arch_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// pkg/plugin-go is a tree of modules, not one module. Each heavy part is its own
// module so an application imports only what it needs:
//
//	contract    standard library only
//	cengine     the C engine binding and pure-Go build of the same API; needs contract
//	wasm        the wazero backend; needs contract only, never cengine
//	hashicorp   the gRPC backend; needs contract only, never cengine
//	(umbrella)  pkg/plugin-go itself, the one module that may require them all
const pluginPrefix = "github.com/webong/ext/pkg/plugin-go"

// A module's allowed requirements among the repository's own modules.
var pluginRequires = map[string][]string{
	"pkg/plugin-go/contract":  {},
	"pkg/plugin-go/cengine":   {"pkg/plugin-go/contract"},
	"pkg/plugin-go/wasm":      {"pkg/plugin-go/contract"},
	"pkg/plugin-go/hashicorp": {"pkg/plugin-go/contract"},
	"pkg/plugin-go":           {"pkg/plugin-go/contract", "pkg/plugin-go/cengine", "pkg/plugin-go/wasm", "pkg/plugin-go/hashicorp", "res/graph"},
}

var requireLine = regexp.MustCompile(`(?m)^\s*(?:require\s+)?(github\.com/webong/ext/\S+)\s+v\S+`)

func TestPluginGoModulesRequireOnlyTheirParts(t *testing.T) {
	root := repoRoot(t)
	for dir, allowed := range pluginRequires {
		data, err := os.ReadFile(filepath.Join(root, dir, "go.mod"))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		okay := map[string]bool{}
		for _, a := range allowed {
			okay["github.com/webong/ext/"+a] = true
		}
		var got []string
		for _, m := range requireLine.FindAllStringSubmatch(string(data), -1) {
			if strings.HasPrefix(m[1], "github.com/webong/ext/") && !okay[m[1]] {
				got = append(got, m[1])
			}
		}
		sort.Strings(got)
		if len(got) > 0 {
			t.Errorf("%s: go.mod requires %v; it may require only %v", dir, got, allowed)
		}
	}
}

// The backends reach the contract and nothing of the C engine, in source as well
// as in go.mod: a wazero or gRPC user must not need a C toolchain or even have the
// engine binding in the module graph.
func TestBackendsNeverImportTheCEngine(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"pkg/plugin-go/wasm", "pkg/plugin-go/hashicorp"} {
		forbidden := []string{pluginPrefix + "/cengine", pluginPrefix}
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
				return walkErr
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				for _, f := range forbidden {
					if name == f || strings.HasPrefix(name, f+"/") && f != pluginPrefix {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s imports %s; a backend depends on pkg/plugin-go/contract only", filepath.ToSlash(rel), name)
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

// pkg/plugin-go/contract is the lean choice: standard library only.
func TestContractModuleUsesOnlyTheStandardLibrary(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "pkg/plugin-go/contract"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir // fixtures, including guests that import backends
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
			name, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(name, "/")
			if strings.Contains(first, ".") && !strings.HasPrefix(name, pluginPrefix+"/contract") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s; the contract module uses only the standard library", filepath.ToSlash(rel), name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
