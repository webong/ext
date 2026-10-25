package cgoengine

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// _csrc is a generated copy of lib/plugin, because a published module contains
// only its own directory. It must never drift from the canonical sources: the iOS,
// Android, Rust and Zig bindings build from lib/plugin, and this module builds
// from the copy. In a checkout of the repository the two trees must be identical;
// a consumer who has only this module has no lib/plugin and the test is skipped.
func TestCSourcesMatchCanonical(t *testing.T) {
	canonical := filepath.Join("..", "..", "..", "lib", "plugin")
	if _, err := os.Stat(filepath.Join(canonical, "CMakeLists.txt")); err != nil {
		t.Skip("lib/plugin is not alongside this module")
	}
	list := func(root string) map[string][]byte {
		files := map[string][]byte{}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if filepath.Base(path) == ".DS_Store" || strings.HasPrefix(filepath.ToSlash(rel), "build/") {
				return nil
			}
			data, err := os.ReadFile(path)
			files[filepath.ToSlash(rel)] = data
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	want, got := list(canonical), list("_csrc")
	var problems []string
	for name, data := range want {
		other, ok := got[name]
		switch {
		case !ok:
			problems = append(problems, "missing from _csrc: "+name)
		case !bytes.Equal(data, other):
			problems = append(problems, "differs: "+name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			problems = append(problems, "not in lib/plugin: "+name)
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if len(problems) > 0 {
		t.Fatal("_csrc has drifted from lib/plugin; run scripts/cgoengine-sync.sh and commit the result")
	}
}
