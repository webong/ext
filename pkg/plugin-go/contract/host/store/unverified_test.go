package store_test

import (
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	"github.com/webong/ext/pkg/plugin-go/contract/package"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// damagedRoot installs two good packages (one a tree) and then breaks the root
// four ways: an unreadable manifest, a directory that holds another package's
// manifest, a stray file and a directory that is not a package ID. A staging
// directory is left in place; it must be ignored.
func damagedRoot(t *testing.T) *store.Store {
	t.Helper()
	s := newStore(t)
	if _, err := s.Install(source(t, "example/a", "r1", "one")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(treePackage(t, nil)); err != nil {
		t.Fatal(err)
	}
	broken, err := s.Install(source(t, "example/broken", "r1", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken.Directory, store.ManifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := s.Install(source(t, "example/moved", "r1", "three"))
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(s.Root(), "example%2fmoved-elsewhere")
	if err := os.Rename(moved.Directory, other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root(), "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.Root(), "Bad Name"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.Root(), ".staging-123"), 0o700); err != nil {
		t.Fatal(err)
	}
	return s
}

func ids(items []store.Installed) []string {
	var out []string
	for _, i := range items {
		out = append(out, i.Manifest.Descriptor.Identity.ID)
	}
	return out
}

func TestListUnverifiedReportsEveryPackage(t *testing.T) {
	s := damagedRoot(t)
	installed, problems, err := s.ListUnverified()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(installed), ","); got != "example/a,example/tree" {
		t.Fatalf("installed %s", got)
	}
	for _, p := range installed {
		if p.Directory == "" || !strings.HasPrefix(p.Directory, s.Root()) {
			t.Fatalf("%s: directory %q", p.Name(), p.Directory)
		}
		if p.Name() != p.Manifest.Descriptor.Identity.ID || !p.Supports("example.read", "v1") || p.Supports("example.read", "v2") {
			t.Fatalf("manifest accessors do not work on %+v", p.Manifest.Descriptor.Identity)
		}
	}
	want := map[string]error{
		"Bad Name":                plugin.ErrInvalid,
		"example/broken":          plugin.ErrInvalid,
		"example/moved-elsewhere": plugin.ErrMismatch,
		"notes.txt":               plugin.ErrInvalid,
	}
	if len(problems) != len(want) {
		t.Fatalf("problems %+v", problems)
	}
	if !sort.SliceIsSorted(problems, func(i, j int) bool { return problems[i].ID < problems[j].ID }) {
		t.Fatalf("problems not sorted: %+v", problems)
	}
	for _, c := range problems {
		kind, ok := want[c.ID]
		if !ok || c.Err == nil || c.OK() || !errors.Is(c.Err, kind) {
			t.Fatalf("%s: %v, want %v", c.ID, c.Err, kind)
		}
		if !strings.HasPrefix(c.Directory, s.Root()) || c.Directory == s.Root() {
			t.Fatalf("%s: directory %q", c.ID, c.Directory)
		}
	}
	// the verifying calls are unchanged: List fails closed on the same root, and
	// Verify names the same bad entries
	if _, err := s.List(); err == nil {
		t.Fatal("List accepted a damaged root")
	}
	checks, err := s.Verify()
	if err != nil {
		t.Fatal(err)
	}
	bad := map[string]bool{}
	for _, c := range checks {
		if c.Err != nil {
			bad[c.ID] = true
		}
	}
	for _, c := range problems {
		if !bad[c.ID] {
			t.Fatalf("Verify did not report %s", c.ID)
		}
	}
}

func TestListUnverifiedReadsNoContent(t *testing.T) {
	s := newStore(t)
	p, err := s.Install(treePackage(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	// change a file inside the tree, add one the manifest does not list, and
	// change the top-level artifact: none of it is looked at
	for name, body := range map[string]string{
		"vendor/LICENSE": "changed", "vendor/extra.txt": "new", "provider.py": "evil",
	} {
		if err := os.WriteFile(filepath.Join(p.Directory, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Load("example/tree"); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("Load: %v", err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List accepted changed content")
	}
	installed, problems, err := s.ListUnverified()
	if err != nil || len(problems) != 0 || len(installed) != 1 {
		t.Fatalf("%v %v %v", installed, problems, err)
	}
	// what it returns is not verified, and says so by failing later
	if err := installed[0].Verify(); err == nil {
		t.Fatal("changed content verified")
	}
	if _, err := installed[0].ArtifactPath("vendor"); err == nil {
		t.Fatal("changed tree artifact served")
	}
}

func TestListUnverifiedMissingRootIsEmptyAndNotCreated(t *testing.T) {
	s := newStore(t)
	installed, problems, err := s.ListUnverified()
	if err != nil || len(installed) != 0 || len(problems) != 0 {
		t.Fatalf("%v %v %v", installed, problems, err)
	}
	if _, err := os.Stat(s.Root()); !os.IsNotExist(err) {
		t.Fatalf("root created: %v", err)
	}
}

func TestLayeredListUnverified(t *testing.T) {
	user, system := newStore(t), newStore(t)
	if _, err := system.Install(source(t, "example/a", "sys", "system")); err != nil {
		t.Fatal(err)
	}
	if _, err := system.Install(source(t, "example/b", "sys", "two")); err != nil {
		t.Fatal(err)
	}
	// a shadowed copy that is damaged must never be read
	sysDamaged, err := system.Install(source(t, "example/c", "sys", "three"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysDamaged.Directory, store.ManifestName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Install(source(t, "example/a", "usr", "user")); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Install(source(t, "example/c", "usr", "three-user")); err != nil {
		t.Fatal(err)
	}
	// a winning copy whose manifest is damaged is reported, not replaced by the
	// system copy
	userDamaged, err := user.Install(source(t, "example/b", "usr", "two-user"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDamaged.Directory, store.ManifestName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(system.Root(), "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	installed, problems, err := layered(t, user, system).ListUnverified()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range installed {
		got[p.Manifest.Descriptor.Identity.ID] = p.Manifest.Descriptor.Identity.Revision
	}
	if fmt.Sprint(got) != "map[example/a:usr example/c:usr]" {
		t.Fatalf("installed %v", got)
	}
	if len(problems) != 2 || problems[0].ID != "example/b" || problems[1].ID != "stray" {
		t.Fatalf("problems %+v", problems)
	}
	if !strings.HasPrefix(problems[0].Directory, user.Root()) || !strings.HasPrefix(problems[1].Directory, system.Root()) {
		t.Fatalf("problems come from the wrong stores: %+v", problems)
	}
}

// bigTree builds a source package whose single tree artifact holds files small
// files, as a stand-in for a vendored runtime with tens of thousands of files.
func bigTree(tb testing.TB, files int) string {
	tb.Helper()
	root := tb.TempDir()
	for i := 0; i < files; i++ {
		path := filepath.Join(root, "vendor", fmt.Sprintf("d%03d", i%200), fmt.Sprintf("f%d.dat", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf("file %d, padded to a size that is not zero............", i)), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "provider.py"), []byte("print(1)"), 0o600); err != nil {
		tb.Fatal(err)
	}
	m, err := packagekit.SourceManifest("example/big", "1.0.0", treeContracts, root, []string{"provider.py", "vendor"})
	if err != nil {
		tb.Fatal(err)
	}
	if err := store.WriteManifest(root, m); err != nil {
		tb.Fatal(err)
	}
	return root
}

// BenchmarkListing compares the three ways to enumerate a store that holds a
// package with a large tree. List and Verify hash every file in it; ListUnverified
// reads one manifest. Run with -benchtime=3x; set EXT_BENCH_TREE_FILES to change
// the 20000 default.
func BenchmarkListing(b *testing.B) {
	files := 20000
	if v := os.Getenv("EXT_BENCH_TREE_FILES"); v != "" {
		if _, err := fmt.Sscan(v, &files); err != nil {
			b.Fatal(err)
		}
	}
	s := newStore(b)
	if _, err := s.Install(bigTree(b, files)); err != nil {
		b.Fatal(err)
	}
	if _, err := s.Install(source(b, "example/small", "r1", "one")); err != nil {
		b.Fatal(err)
	}
	b.Run("ListUnverified", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			installed, problems, err := s.ListUnverified()
			if err != nil || len(problems) != 0 || len(installed) != 2 {
				b.Fatal(installed, problems, err)
			}
		}
	})
	b.Run("List", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if items, err := s.List(); err != nil || len(items) != 2 {
				b.Fatal(items, err)
			}
		}
	})
	b.Run("Verify", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if checks, err := s.Verify(); err != nil || len(checks) != 2 {
				b.Fatal(checks, err)
			}
		}
	})
}
