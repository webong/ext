package arch_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The extension JavaScript library is owned by ext and lives in pkg/ext-web, with
// the other packages for code that runs in a platform of its own. It is not a
// source module of res/web and has no staging link there.
func TestWebExtLocation(t *testing.T) {
	root := repoRoot(t)
	for _, old := range []string{"pkg/ext-web-js", "res/web/js"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(old))); err == nil {
			t.Fatalf("%s must not exist; the library lives in pkg/ext-web", old)
		}
	}
	dir := filepath.Join(root, "pkg", "ext-web")
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Name    string `json:"name"`
		Private bool   `json:"private"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "ext-web" || !pkg.Private {
		t.Errorf("package.json is %q (private=%v); the library is ext-web and stays private until a registry is chosen", pkg.Name, pkg.Private)
	}
	walkWebJS(t, dir, func(path string) {
		if name := filepath.Base(path); name == "go.mod" || strings.HasSuffix(name, ".go") {
			t.Errorf("%s: pkg/ext-web is not a Go package or module", path)
		}
	})
}

// The library runs inside a browser extension, so it may use only Node built-ins
// (for tests and example builds) and its own files. It never reaches the plugin
// engine, the TypeScript binding or an adapter.
func TestWebExtStandsAlone(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "pkg", "ext-web")
	var pkg map[string]json.RawMessage
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
		if _, ok := pkg[key]; ok {
			t.Errorf("package.json declares %s; pkg/ext-web has no dependencies", key)
		}
	}
	specifier := regexp.MustCompile(`\b(?:from|import)\s*\(?\s*['"]([^'"]+)['"]`)
	walkWebJS(t, dir, func(path string) {
		switch filepath.Ext(path) {
		case ".mjs", ".mts", ".js":
		default:
			return
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			return
		}
		for _, m := range specifier.FindAllStringSubmatch(string(source), -1) {
			spec := m[1]
			switch {
			case strings.HasPrefix(spec, "node:"):
			case strings.HasPrefix(spec, "."):
				target := filepath.Join(filepath.Dir(path), spec)
				if rel, err := filepath.Rel(dir, target); err != nil || strings.HasPrefix(rel, "..") {
					t.Errorf("%s imports %q outside pkg/ext-web", path, spec)
				}
			default:
				t.Errorf("%s imports %q; only node: built-ins and relative files are allowed", path, spec)
			}
		}
	})
}

// walkWebJS visits the files of the library, skipping installed and generated trees.
func walkWebJS(t *testing.T, dir string, visit func(path string)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		visit(path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
