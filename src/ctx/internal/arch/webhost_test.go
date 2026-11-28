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

const webHostModule = "github.com/webong/ext/src/ctx/web"

// src/ctx/web holds ctx's web contracts as packages of the ctx module, versioned with it rather
// than tagged on their own. Its group directories (manage, artifact, engine) hold packages, not
// Go files of their own.
func TestWebHostModulePath(t *testing.T) {
	root := repoRoot(t)
	web := filepath.Join(root, "src", "ctx", "web")
	if _, err := os.Stat(filepath.Join(web, "go.mod")); err == nil {
		t.Fatal("src/ctx/web is part of the ctx module and has no go.mod of its own")
	}
	for _, group := range []string{"manage", "artifact", "engine"} {
		files, err := os.ReadDir(filepath.Join(web, group))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".go") {
				t.Errorf("src/ctx/web/%s/%s: a group directory holds packages, not Go files", group, file.Name())
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
	host := filepath.Join(root, "src", "ctx", "web")
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

func TestWebAssemblyContractDoesNotOwnWazero(t *testing.T) {
	root := repoRoot(t)
	core := filepath.Join(root, "pkg", "ext-go", "core")
	contract := filepath.Join(core, "wasm")
	mod, err := os.ReadFile(filepath.Join(core, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mod), "github.com/tetratelabs/wazero") {
		t.Fatal("pkg/ext-go/core must leave wazero to pkg/ext-go/wasm/runtime")
	}
	for _, dir := range []string{
		contract,
		filepath.Join(root, "pkg", "ext-go", "wasm", "runtime"),
	} {
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return walkErr
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if dir == contract && name == "github.com/tetratelabs/wazero" ||
					dir != contract && strings.Contains(name, "/plugin") {
					t.Errorf("%s imports %s across the WebAssembly contract boundary", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The web contracts serve consumers without the plugin engine (mobile, WebAssembly, browser
// adapters), so no package under src/ctx/web imports an engine.
func TestWebContractsImportNoEngine(t *testing.T) {
	root := repoRoot(t)
	web := filepath.Join(root, "src", "ctx", "web")
	engines := []string{
		"github.com/webong/ext/pkg/ext-go/engine",
		"github.com/webong/ext/pkg/ext-go/cgoengine",
		"github.com/webong/ext/pkg/ext-go/c2goengine",
		"github.com/webong/ext/pkg/ext-go/cengine",
	}
	err := filepath.WalkDir(web, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return walkErr
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			for _, engine := range engines {
				if name == engine || strings.HasPrefix(name, engine+"/") {
					t.Errorf("%s imports %s; the web contracts must not depend on a plugin engine", path, name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
