//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The C store's whole-root reports and its layered view are held against the pure-Go store, the
// baseline. Reports change nothing, so both implementations read the same roots, which a test
// damages one way after another; at each stage they must give the same lines, in the same order,
// with the same errors.

type storeReports interface {
	List() ([]PackageRecord, error)
	Verify() ([]PackageCheck, error)
	ListUnverified() ([]PackageRecord, []PackageCheck, error)
}

func describeRecords(records []PackageRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, fmt.Sprintf("  %s layer=%d dir=%s manifest=%x", r.ID, r.Layer, r.Directory, sha256.Sum256(r.Manifest)))
	}
	return out
}

func describeChecks(checks []PackageCheck) []string {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		out = append(out, fmt.Sprintf("  %s layer=%d dir=%s %s", c.ID, c.Layer, c.Directory, storeClass(c.Err)))
	}
	return out
}

func describeReports(r storeReports) []string {
	var out []string
	records, err := r.List()
	out = append(out, "list: "+storeClass(err))
	out = append(out, describeRecords(records)...)
	checks, err := r.Verify()
	out = append(out, "verify: "+storeClass(err))
	out = append(out, describeChecks(checks)...)
	records, problems, err := r.ListUnverified()
	out = append(out, "unverified: "+storeClass(err))
	out = append(out, describeRecords(records)...)
	out = append(out, "problems:")
	out = append(out, describeChecks(problems)...)
	return out
}

func diffLines(t *testing.T, stage string, want, got []string) {
	t.Helper()
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Errorf("%s differs\n--- go\n%s\n--- c\n%s", stage, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// planter puts packages into roots through the pure-Go store.
type planter struct {
	t    *testing.T
	work string
}

func (p planter) install(root string, spec pkgSpec) {
	p.t.Helper()
	src := writeStoreSource(p.t, p.work, spec)
	s, err := openGoPackageStore(root, PackageStoreLimits{})
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := s.Install(src, false); err != nil {
		p.t.Fatalf("install %s into %s: %v", spec.id, root, err)
	}
}

func dirOf(root, id string) string {
	return filepath.Join(root, strings.ReplaceAll(id, "/", "%2f"))
}

func (p planter) write(path, body string) {
	p.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		p.t.Fatal(err)
	}
}

func (p planter) copyDir(from, to string) {
	p.t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o600)
	})
	if err != nil {
		p.t.Fatal(err)
	}
}

func spec(id, revision, body string) pkgSpec {
	return pkgSpec{id: id, revision: revision, files: []pkgFile{{path: "src/main.py", body: body}}}
}

func TestPackageStoreReportsMatchGo(t *testing.T) {
	work := t.TempDir()
	p := planter{t: t, work: filepath.Join(work, "src")}
	root := filepath.Join(work, "store")
	g, err := openGoPackageStore(root, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := OpenPackageStore(root, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	check := func(stage string) {
		t.Helper()
		want, got := describeReports(g), describeReports(c)
		diffLines(t, stage, want, got)
		if testing.Verbose() {
			t.Logf("%s: %d lines", stage, len(got))
		}
	}

	check("a root that is not there")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	check("an empty root")
	p.install(root, spec("example/a", "r1", "one"))
	p.install(root, spec("deep/er/b", "r1", "two"))
	p.install(root, spec("c", "r1", "three"))
	p.install(root, pkgSpec{id: "z.tree", revision: "r1", files: []pkgFile{{path: "f", body: "x"}}, tree: []pkgFile{{path: "x.txt", body: "ex"}, {path: "sub/y.txt", body: "why"}}})
	check("four packages")

	p.write(filepath.Join(dirOf(root, "deep/er/b"), "src", "main.py"), "changed")
	check("a changed artifact")
	p.write(filepath.Join(dirOf(root, "example/a"), "unlisted.txt"), "extra")
	check("an unlisted file")
	p.write(filepath.Join(dirOf(root, "c"), "plugin.json"), "{")
	check("a manifest that does not decode")
	p.write(filepath.Join(dirOf(root, "z.tree"), "data", "sub", "y.txt"), "different")
	check("a changed file in a tree")

	p.write(filepath.Join(root, "stray.txt"), "x")
	if err := os.Mkdir(filepath.Join(root, "Not A Package"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".install-leftover"), 0o700); err != nil {
		t.Fatal(err)
	}
	p.copyDir(dirOf(root, "example/a"), filepath.Join(root, "other"))
	check("entries that are not packages")

	if runtime.GOOS != "windows" {
		if err := os.Symlink(dirOf(root, "example/a"), filepath.Join(root, "linked")); err != nil {
			t.Fatal(err)
		}
		check("a link to a package")
		if err := os.Remove(filepath.Join(root, "linked")); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"stray.txt", "Not A Package", "other"} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	check("the entries removed")
	for _, id := range []string{"deep/er/b", "c", "z.tree", "example/a"} {
		if err := os.RemoveAll(dirOf(root, id)); err != nil {
			t.Fatal(err)
		}
	}
	check("damaged packages removed")
}

func TestPackageStoreReportsClosedAndMisplaced(t *testing.T) {
	s, err := OpenPackageStore(filepath.Join(t.TempDir(), "s"), PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := s.List(); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("List on a closed store: %v", err)
	}
	if _, err := s.Verify(); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("Verify on a closed store: %v", err)
	}
	if _, _, err := s.ListUnverified(); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("ListUnverified on a closed store: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenPackageStore(file, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.List(); err == nil {
		t.Error("List of a root that is a file succeeded")
	}
	if _, err := f.Verify(); err == nil {
		t.Error("Verify of a root that is a file succeeded")
	}
}

func openBoth(t *testing.T, roots []string) (*goLayeredPackageStore, *LayeredPackageStore) {
	t.Helper()
	g, err := openGoLayeredPackageStore(roots, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := OpenLayeredPackageStore(roots, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return g, c
}

func describeLoad(id string, r interface {
	Load(string) (PackageRecord, error)
}) string {
	rec, err := r.Load(id)
	return fmt.Sprintf("load %q: %s layer=%d dir=%s manifest=%x", id, storeClass(err), rec.Layer, rec.Directory, sha256.Sum256(rec.Manifest))
}

func TestLayeredPackageStoreMatchesGo(t *testing.T) {
	work := t.TempDir()
	p := planter{t: t, work: filepath.Join(work, "src")}
	user, system, absent := filepath.Join(work, "user"), filepath.Join(work, "system"), filepath.Join(work, "absent")
	p.install(system, spec("only/system", "s1", "sys"))
	p.install(system, spec("shadowed", "system", "from the system"))
	p.install(system, spec("also/shadowed", "system", "also from the system"))
	p.install(user, spec("shadowed", "user", "from the user"))
	p.install(user, spec("only/user", "u1", "usr"))
	p.install(user, spec("also/shadowed", "user", "also from the user"))

	g, c := openBoth(t, []string{user, absent, system})
	if got := c.Roots(); strings.Join(got, "|") != strings.Join([]string{user, absent, system}, "|") {
		t.Errorf("Roots = %v", got)
	}
	ids := []string{"only/system", "only/user", "shadowed", "also/shadowed", "nowhere", "Not Valid", "", "../x"}
	check := func(stage string) {
		t.Helper()
		var want, got []string
		for _, id := range ids {
			want = append(want, describeLoad(id, g))
			got = append(got, describeLoad(id, c))
		}
		want = append(want, describeReports(g)...)
		got = append(got, describeReports(c)...)
		diffLines(t, stage, want, got)
		if testing.Verbose() {
			t.Logf("%s: %d lines", stage, len(got))
		}
	}

	check("two roots, one shadowing the other")
	p.write(filepath.Join(dirOf(system, "shadowed"), "src", "main.py"), "damaged shadowed copy")
	check("the shadowed copy damaged")
	p.write(filepath.Join(dirOf(user, "shadowed"), "src", "main.py"), "damaged winner")
	check("the winning copy damaged")
	p.write(filepath.Join(dirOf(user, "also/shadowed"), "plugin.json"), "{")
	check("the winning manifest unreadable")
	p.write(filepath.Join(dirOf(system, "only/system"), "extra.txt"), "x")
	check("a package damaged in the lower root")
	p.write(filepath.Join(system, "junk"), "x")
	check("an entry that is not a package in the lower root")
	p.write(filepath.Join(user, "junk"), "x")
	check("an entry that is not a package in both roots")
	if err := os.Mkdir(filepath.Join(system, ".install-x"), 0o700); err != nil {
		t.Fatal(err)
	}
	check("a staging directory")
	if err := os.Remove(filepath.Join(user, "junk")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(system, "junk")); err != nil {
		t.Fatal(err)
	}
	p.copyDir(dirOf(user, "only/user"), filepath.Join(system, "mismatch"))
	check("a directory whose manifest names another id")
}

func TestLayeredPackageStoreRefusals(t *testing.T) {
	work := t.TempDir()
	a, b := filepath.Join(work, "a"), filepath.Join(work, "b")
	cases := []struct {
		name  string
		roots []string
	}{
		{"none", nil},
		{"empty list", []string{}},
		{"empty root", []string{a, ""}},
		{"repeated root", []string{a, b, a}},
		{"repeated by a dot", []string{a, a + string(filepath.Separator) + "."}},
		{"repeated by a slash", []string{a, a + string(filepath.Separator)}},
		{"repeated by a parent", []string{a, filepath.Join(a, "..", "a")}},
		{"fine", []string{a, b}},
		{"fine, one root", []string{a}},
	}
	for _, tc := range cases {
		_, goErr := openGoLayeredPackageStore(tc.roots, PackageStoreLimits{})
		c, cErr := OpenLayeredPackageStore(tc.roots, PackageStoreLimits{})
		if c != nil {
			c.Close()
		}
		if storeClass(goErr) != storeClass(cErr) {
			t.Errorf("%s: go %s, c %s", tc.name, storeClass(goErr), storeClass(cErr))
		}
	}
	if _, err := OpenLayeredPackageStore([]string{a, "x\x00y"}, PackageStoreLimits{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("a root with a NUL: %v", err)
	}
	if _, err := OpenLayeredPackageStore([]string{a}, PackageStoreLimits{MaxTreeFiles: -1}); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("a negative limit: %v", err)
	}
	c, err := OpenLayeredPackageStore([]string{a}, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	c.Close()
	if _, err := c.Load("x"); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("Load on a closed view: %v", err)
	}
	if _, err := c.List(); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("List on a closed view: %v", err)
	}
	if _, err := c.Verify(); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("Verify on a closed view: %v", err)
	}
	if _, _, err := c.ListUnverified(); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("ListUnverified on a closed view: %v", err)
	}
}
