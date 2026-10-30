//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// The C package store is held against the package store of the pure-Go engine, the baseline: the
// same operations on two roots, which must give the same outcome for each, the same installed
// manifest bytes, and the same directory below the root at the end (names, contents and, where the
// platform has them, permission bits).

type packageStoreOps interface {
	Install(source string, preserveExecutable bool) ([]byte, error)
	Upgrade(source string, preserveExecutable bool) ([]byte, error)
	Replace(source string, preserveExecutable bool) ([]byte, error)
	Remove(id string) error
	Load(id string) ([]byte, error)
	IDs() ([]string, error)
	Close()
}

// pkgSpec describes a source package. Files are written as given; the artifacts are the files
// listed by name, and Tree is a directory artifact.
type pkgSpec struct {
	id, revision, contract string
	files                  []pkgFile // file artifacts, named by their position
	tree                   []pkgFile // the files of the tree artifact "data"
	entrypoint             int       // 1-based index of the file artifact that is an entrypoint, 0 for none
	indent                 string    // how plugin.json is written: "" compact, otherwise the indent
	extra                  map[string]string
}
type pkgFile struct {
	path, body string
	exec       bool
}

func writeStoreSource(t *testing.T, root string, spec pkgSpec) string {
	t.Helper()
	if spec.contract == "" {
		spec.contract = "example.read"
	}
	dir := filepath.Join(root, strings.ReplaceAll(spec.id, "/", "_")+"-"+spec.revision)
	put := func(rel, body string, exec bool) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if exec {
			mode = 0o700
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	m := packagekit.Manifest{APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: spec.id, Revision: spec.revision},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: spec.contract, Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}}}
	for i, f := range spec.files {
		put(f.path, f.body, f.exec)
		sum := sha256.Sum256([]byte(f.body))
		m.Artifacts = append(m.Artifacts, packagekit.Artifact{Name: fmt.Sprintf("a%d", i+1), Path: f.path, SHA256: hex.EncodeToString(sum[:])})
	}
	if len(spec.tree) > 0 {
		for _, f := range spec.tree {
			put("data/"+f.path, f.body, f.exec)
		}
		digest, err := plugin.DirectoryDigest(filepath.Join(dir, "data"))
		if err != nil {
			t.Fatal(err)
		}
		m.Artifacts = append(m.Artifacts, packagekit.Artifact{Name: "data", Path: "data", SHA256: digest, Kind: packagekit.ArtifactTree})
	}
	if spec.entrypoint > 0 {
		m.Entrypoints = []packagekit.Entrypoint{{Name: "default", Runtime: "jsonline", Artifact: fmt.Sprintf("a%d", spec.entrypoint), Protocols: []string{plugin.APIVersion}}}
	}
	for rel, body := range spec.extra {
		put(rel, body, false)
	}
	var raw []byte
	var err error
	if spec.indent == "" {
		raw, err = json.Marshal(m)
	} else {
		raw, err = json.MarshalIndent(m, "", spec.indent)
		raw = append(raw, "\r\n\r\n"...) // not what the Go store would write
	}
	if err != nil {
		t.Fatal(err)
	}
	put("plugin.json", string(raw), false)
	return dir
}

func storeClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrPackageExists):
		return "exists"
	case errors.Is(err, plugin.ErrNotFound):
		return "not found"
	case errors.Is(err, plugin.ErrMismatch):
		return "mismatch"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	}
	return "other"
}

// snapshot lists everything below root: kind, permission bits and content digest.
func storeTree(t *testing.T, root string) []string {
	t.Helper()
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return []string{"no root"}
	}
	lines := []string{"root"}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		mode := fmt.Sprintf("%04o", info.Mode().Perm())
		if runtime.GOOS == "windows" {
			mode = "----"
		}
		switch {
		case info.IsDir():
			lines = append(lines, fmt.Sprintf("dir  %s %s", mode, filepath.ToSlash(rel)))
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			lines = append(lines, fmt.Sprintf("file %s %s %s", mode, filepath.ToSlash(rel), hex.EncodeToString(sum[:8])))
		default:
			lines = append(lines, fmt.Sprintf("other %s", filepath.ToSlash(rel)))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return lines
}

// run is what a scenario sees: the store, a place to build sources, and a transcript.
type run struct {
	t          *testing.T
	s          packageStoreOps
	root, work string
	log        []string
}

func (r *run) source(spec pkgSpec) string { return writeStoreSource(r.t, r.work, spec) }
func (r *run) note(format string, args ...any) {
	r.log = append(r.log, fmt.Sprintf(format, args...))
}
func (r *run) install(name, source string, preserve bool) {
	m, err := r.s.Install(source, preserve)
	r.note("install %s: %s %x", name, storeClass(err), sha256.Sum256(m))
}
func (r *run) upgrade(name, source string, preserve bool) {
	m, err := r.s.Upgrade(source, preserve)
	r.note("upgrade %s: %s %x", name, storeClass(err), sha256.Sum256(m))
}
func (r *run) replace(name, source string, preserve bool) {
	m, err := r.s.Replace(source, preserve)
	r.note("replace %s: %s %x", name, storeClass(err), sha256.Sum256(m))
}
func (r *run) remove(id string) { r.note("remove %s: %s", id, storeClass(r.s.Remove(id))) }
func (r *run) load(id string) {
	m, err := r.s.Load(id)
	r.note("load %s: %s %x", id, storeClass(err), sha256.Sum256(m))
}
func (r *run) ids() {
	ids, err := r.s.IDs()
	r.note("ids: %s %v", storeClass(err), ids)
}

type packageStoreScenario struct {
	name   string
	limits PackageStoreLimits
	run    func(r *run)
}

func (c packageStoreScenario) play(t *testing.T, open func(root string, l PackageStoreLimits) (packageStoreOps, error)) (log, tree []string) {
	t.Helper()
	base := t.TempDir()
	r := &run{t: t, root: filepath.Join(base, "store"), work: filepath.Join(base, "work")}
	if err := os.MkdirAll(r.work, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := open(r.root, c.limits)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r.s = s
	c.run(r)
	return r.log, storeTree(t, r.root)
}

func openGoOps(root string, l PackageStoreLimits) (packageStoreOps, error) {
	s, err := openGoPackageStore(root, l)
	return s, err
}
func openCOps(root string, l PackageStoreLimits) (packageStoreOps, error) {
	s, err := OpenPackageStore(root, l)
	return s, err
}

func readStoreFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var packageStoreScenarios = []packageStoreScenario{
	{name: "lifecycle", run: func(r *run) {
		r.ids()
		r.load("example/a")
		r.remove("example/a")
		r.upgrade("missing", r.source(pkgSpec{id: "example/a", revision: "r1", files: []pkgFile{{path: "src/main.py", body: "one"}}}), false)
		r.install("a r1", r.source(pkgSpec{id: "example/a", revision: "r1", files: []pkgFile{{path: "src/main.py", body: "one"}}}), false)
		r.install("a again", r.source(pkgSpec{id: "example/a", revision: "r2", files: []pkgFile{{path: "src/main.py", body: "two"}}}), false)
		r.ids()
		r.load("example/a")
		r.upgrade("a same revision", r.source(pkgSpec{id: "example/a", revision: "r1", files: []pkgFile{{path: "src/main.py", body: "other"}}}), false)
		r.upgrade("a other contracts", r.source(pkgSpec{id: "example/a", revision: "r3", contract: "example.write", files: []pkgFile{{path: "src/main.py", body: "x"}}}), false)
		r.upgrade("a r2", r.source(pkgSpec{id: "example/a", revision: "r2", files: []pkgFile{{path: "src/main.py", body: "two"}}}), false)
		r.load("example/a")
		r.replace("a other contracts", r.source(pkgSpec{id: "example/a", revision: "r4", contract: "example.write", files: []pkgFile{{path: "src/main.py", body: "three"}, {path: "lib/util.py", body: "u"}}}), false)
		r.replace("b new", r.source(pkgSpec{id: "example/b", revision: "r1", files: []pkgFile{{path: "src/main.py", body: "bee"}}}), false)
		r.ids()
		r.remove("example/a")
		r.remove("example/a")
		r.remove("../a")
		r.ids()
		r.load("Bad")
		r.load("example/a")
	}},
	{name: "ids with slashes and dots", run: func(r *run) {
		for _, id := range []string{"a", "a.b-c_d", "deep/er/name", "0lead"} {
			r.install(id, r.source(pkgSpec{id: id, revision: "r1", files: []pkgFile{{path: "f", body: id}}}), false)
		}
		r.ids()
		r.load("deep/er/name")
	}},
	{name: "the manifest is copied as it was", run: func(r *run) {
		r.install("indented", r.source(pkgSpec{id: "example/tab", revision: "r1", indent: "\t", files: []pkgFile{{path: "f", body: "x"}}}), false)
		r.install("spaces", r.source(pkgSpec{id: "example/sp", revision: "r1", indent: "      ", files: []pkgFile{{path: "f", body: "x"}}}), false)
		r.load("example/tab")
	}},
	{name: "trees and permissions", run: func(r *run) {
		spec := pkgSpec{id: "example/tree", revision: "r1", files: []pkgFile{{path: "bin/tool", body: "#!/bin/sh\n", exec: true}, {path: "src/main.py", body: "m"}},
			tree: []pkgFile{{path: "x.txt", body: "ex"}, {path: "sub/y.txt", body: "why"}, {path: "run.sh", body: "#!/bin/sh\n", exec: true}, {path: "sub/deeper/z", body: "z", exec: true}}}
		r.install("default", r.source(spec), false)
		spec.id, spec.revision = "example/tree2", "r1"
		r.install("preserve", r.source(spec), true)
		spec.id, spec.entrypoint = "example/tree3", 1
		r.install("entrypoint", r.source(spec), false)
		r.replace("entrypoint preserved", r.source(spec), true)
		r.load("example/tree3")
	}},
	{name: "source defects", run: func(r *run) {
		good := pkgSpec{id: "example/src", revision: "r1", files: []pkgFile{{path: "src/main.py", body: "m"}}}
		r.install("extra file", r.source(pkgSpec{id: "example/x1", revision: "r1", files: good.files, extra: map[string]string{"stray.txt": "unlisted"}}), false)
		dir := r.source(pkgSpec{id: "example/x2", revision: "r1", files: good.files})
		os.WriteFile(filepath.Join(dir, "src", "main.py"), []byte("tampered"), 0o600)
		r.install("tampered", dir, false)
		dir = r.source(pkgSpec{id: "example/x3", revision: "r1", files: good.files})
		os.Remove(filepath.Join(dir, "src", "main.py"))
		r.install("missing artifact", dir, false)
		dir = r.source(pkgSpec{id: "example/x4", revision: "r1", files: good.files})
		os.Remove(filepath.Join(dir, "plugin.json"))
		r.install("no manifest", dir, false)
		dir = r.source(pkgSpec{id: "example/x5", revision: "r1", files: good.files})
		os.WriteFile(filepath.Join(dir, "plugin.json"), []byte("{"), 0o600)
		r.install("bad manifest", dir, false)
		dir = r.source(pkgSpec{id: "example/x6", revision: "r1", files: good.files})
		raw := readStoreFile(r.t, filepath.Join(dir, "plugin.json"))
		os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(strings.Replace(raw, `"artifacts"`, `"unknown":1,"artifacts"`, 1)), 0o600)
		r.install("unknown member", dir, false)
		dir = r.source(pkgSpec{id: "example/x7", revision: "r1", files: good.files})
		raw = readStoreFile(r.t, filepath.Join(dir, "plugin.json"))
		os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(strings.Replace(raw, "example/x7", "Example/X7", 1)), 0o600)
		r.install("bad id", dir, false)
		r.install("not a directory", filepath.Join(dir, "plugin.json"), false)
		r.install("no such directory", filepath.Join(r.work, "nowhere"), false)
		for _, name := range []string{"plugin.json", "Plugin.JSON"} {
			d := r.source(pkgSpec{id: "example/res", revision: "r1", files: []pkgFile{{path: "src/main.py", body: "m"}}})
			raw := readStoreFile(r.t, filepath.Join(d, "plugin.json"))
			os.WriteFile(filepath.Join(d, "plugin.json"), []byte(strings.Replace(raw, `"src/main.py"`, `"`+name+`"`, 1)), 0o600)
			r.install("manifest as artifact "+name, d, false)
		}
		d := r.source(pkgSpec{id: "example/tx", revision: "r1", files: good.files, tree: []pkgFile{{path: "a", body: "a"}, {path: "sub/b", body: "b"}}})
		os.WriteFile(filepath.Join(d, "data", "sub", "b"), []byte("changed"), 0o600)
		r.install("tampered tree", d, false)
		d = r.source(pkgSpec{id: "example/ty", revision: "r1", files: good.files, tree: []pkgFile{{path: "a", body: "a"}}})
		os.WriteFile(filepath.Join(d, "data", "late"), []byte("late"), 0o600)
		r.install("file added to tree", d, false)
		if runtime.GOOS != "windows" {
			d = r.source(pkgSpec{id: "example/ln", revision: "r1", files: good.files})
			os.Remove(filepath.Join(d, "src", "main.py"))
			os.Symlink(filepath.Join(d, "plugin.json"), filepath.Join(d, "src", "main.py"))
			r.install("symlinked artifact", d, false)
			d = r.source(pkgSpec{id: "example/ln2", revision: "r1", files: good.files, tree: []pkgFile{{path: "a", body: "a"}}})
			os.Symlink(filepath.Join(d, "plugin.json"), filepath.Join(d, "data", "link"))
			r.install("symlink in tree", d, false)
			d = r.source(pkgSpec{id: "example/ln3", revision: "r1", files: good.files})
			os.Symlink(".", filepath.Join(d, "loop"))
			r.install("stray symlink", d, false)
		}
		r.ids()
	}},
	{name: "a failed upgrade leaves the package", run: func(r *run) {
		r.install("a", r.source(pkgSpec{id: "example/a", revision: "r1", files: []pkgFile{{path: "src/main.py", body: "one"}}}), false)
		dir := r.source(pkgSpec{id: "example/a", revision: "r2", files: []pkgFile{{path: "src/main.py", body: "two"}}})
		os.WriteFile(filepath.Join(dir, "src", "main.py"), []byte("tampered"), 0o600)
		r.upgrade("tampered", dir, false)
		r.upgrade("contracts", r.source(pkgSpec{id: "example/a", revision: "r3", contract: "example.other", files: []pkgFile{{path: "src/main.py", body: "x"}}}), false)
		r.load("example/a")
	}},
	{name: "limits on one artifact", limits: PackageStoreLimits{MaxArtifactBytes: 4}, run: func(r *run) {
		r.install("fits", r.source(pkgSpec{id: "example/small", revision: "r1", files: []pkgFile{{path: "f", body: "four"}}}), false)
		r.install("one byte over", r.source(pkgSpec{id: "example/big", revision: "r1", files: []pkgFile{{path: "f", body: "fives"}}}), false)
		r.install("tree file over", r.source(pkgSpec{id: "example/bigtree", revision: "r1", files: []pkgFile{{path: "f", body: "ok"}}, tree: []pkgFile{{path: "a", body: "too big"}}}), false)
		r.ids()
	}},
	{name: "limits on a tree", limits: PackageStoreLimits{MaxTreeFiles: 2, MaxTreeBytes: 7}, run: func(r *run) {
		tree := func(id string, bodies ...string) string {
			var files []pkgFile
			for i, b := range bodies {
				files = append(files, pkgFile{path: fmt.Sprintf("d%d/f", i), body: b})
			}
			return r.source(pkgSpec{id: id, revision: "r1", files: []pkgFile{{path: "f", body: "ok"}}, tree: files})
		}
		r.install("two files", tree("example/t2", "abc", "defg"), false)
		r.install("three files", tree("example/t3", "a", "b", "c"), false)
		r.install("eight bytes", tree("example/t8", "abcd", "efgh"), false)
		r.ids()
	}},
	{name: "directories that are not what their name says", run: func(r *run) {
		r.install("a", r.source(pkgSpec{id: "example/a", revision: "r1", files: []pkgFile{{path: "f", body: "a"}}}), false)
		r.install("b", r.source(pkgSpec{id: "example/b", revision: "r1", files: []pkgFile{{path: "f", body: "b"}}}), false)
		must := func(err error) {
			if err != nil {
				r.t.Fatal(err)
			}
		}
		must(os.Rename(filepath.Join(r.root, "example%2fb"), filepath.Join(r.root, "example%2fz")))
		r.ids()
		r.load("example/z")
		r.remove("example/z")
		r.load("example/b")
		must(os.Rename(filepath.Join(r.root, "example%2fz"), filepath.Join(r.root, "example%2fb")))
		must(os.WriteFile(filepath.Join(r.root, "example%2fa", "src.txt"), []byte("stray"), 0o600))
		r.load("example/a")
		r.install("a over a damaged package", r.source(pkgSpec{id: "example/a", revision: "r2", files: []pkgFile{{path: "f", body: "a2"}}}), false)
		r.replace("a over a damaged package", r.source(pkgSpec{id: "example/a", revision: "r2", files: []pkgFile{{path: "f", body: "a2"}}}), false)
		r.load("example/a")
		must(os.WriteFile(filepath.Join(r.root, "stray"), []byte("x"), 0o600))
		r.ids()
		r.load("example/a")
		must(os.Remove(filepath.Join(r.root, "stray")))
		must(os.Mkdir(filepath.Join(r.root, ".install-left"), 0o700))
		r.ids()
		must(os.Mkdir(filepath.Join(r.root, "Not-A-Package"), 0o700))
		r.ids()
		must(os.Remove(filepath.Join(r.root, "Not-A-Package")))
		must(os.WriteFile(filepath.Join(r.root, "example%2fb", "plugin.json"), []byte("{}"), 0o600))
		r.load("example/b")
		r.remove("example/b")
		r.remove("example/a")
	}},
}

func TestPackageStoreMatchesGo(t *testing.T) {
	for _, c := range packageStoreScenarios {
		t.Run(c.name, func(t *testing.T) {
			wantLog, wantTree := c.play(t, openGoOps)
			gotLog, gotTree := c.play(t, openCOps)
			for i := 0; i < len(wantLog) || i < len(gotLog); i++ {
				var w, g string
				if i < len(wantLog) {
					w = wantLog[i]
				}
				if i < len(gotLog) {
					g = gotLog[i]
				}
				if w != g {
					t.Errorf("step %d:\n  go: %s\n  c:  %s", i, w, g)
				}
			}
			if strings.Join(wantTree, "\n") != strings.Join(gotTree, "\n") {
				t.Errorf("directories differ\n--- go\n%s\n--- c\n%s", strings.Join(wantTree, "\n"), strings.Join(gotTree, "\n"))
			}
			if testing.Verbose() {
				t.Logf("%d steps, %d entries", len(gotLog), len(gotTree))
			}
		})
	}
}

// A closed store refuses every call; a store whose root is a file is not a store.
func TestPackageStoreClosedAndMisplaced(t *testing.T) {
	s, err := OpenPackageStore(filepath.Join(t.TempDir(), "s"), PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s.Close()
	if _, err := s.IDs(); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := s.Install("x", false); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
	if err := s.Remove("x"); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		root   string
		limits PackageStoreLimits
	}{{"", PackageStoreLimits{}}, {"x\x00y", PackageStoreLimits{}}, {"r", PackageStoreLimits{MaxArtifactBytes: -1}}, {"r", PackageStoreLimits{MaxTreeFiles: -1}}, {"r", PackageStoreLimits{MaxTreeBytes: -1}}} {
		if _, err := OpenPackageStore(bad.root, bad.limits); !errors.Is(err, plugin.ErrInvalid) {
			t.Errorf("OpenPackageStore(%q, %+v) = %v, want ErrInvalid", bad.root, bad.limits, err)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPackageStore(file, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.IDs(); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("IDs on a file root: %v", err)
	}
	if _, err := s.Install(writeStoreSource(t, t.TempDir(), pkgSpec{id: "example/a", revision: "r1", files: []pkgFile{{path: "f", body: "a"}}}), false); !errors.Is(err, plugin.ErrInvalid) && storeClass(err) == "ok" {
		t.Errorf("Install into a file root: %v", err)
	}
}
