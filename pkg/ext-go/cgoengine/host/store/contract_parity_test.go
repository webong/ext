package store_test

import (
	"errors"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cgoengine/host/store"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	cstore "github.com/webong/ext/pkg/ext-go/engine/host/store"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// contract/store is the pure-Go implementation and this package the engine-routed
// one, with the same API. They must never drift: every scenario below runs through
// both and compares what they report, error classes included.
type installedAPI interface {
	Name() string
	Version() string
	Digest() (string, error)
	Verify() error
	ArtifactPath(string) (string, error)
	Executable() (string, error)
	IsExecutable() bool
}

type storeAPI struct {
	root     string
	install  func(string) (installedAPI, error)
	upgrade  func(string) (installedAPI, error)
	list     func() ([]installedAPI, error)
	load     func(string) (installedAPI, error)
	remove   func(string) error
	verify   func() (string, error)
	layered  func(other storeAPI) (string, error)
	classify func(error) string
}

func classes(err error, own ...struct {
	name string
	err  error
}) string {
	if err == nil {
		return "ok"
	}
	var out []string
	for _, c := range append([]struct {
		name string
		err  error
	}{{"invalid", plugin.ErrInvalid}, {"notfound", plugin.ErrNotFound}, {"mismatch", plugin.ErrMismatch}, {"denied", plugin.ErrDenied}, {"unsupported", plugin.ErrUnsupported}, {"nonexistent", os.ErrNotExist}}, own...) {
		if errors.Is(err, c.err) {
			out = append(out, c.name)
		}
	}
	if len(out) == 0 {
		return "other"
	}
	return strings.Join(out, "+")
}

func newEngineAPI(t *testing.T, limits store.Limits) storeAPI {
	root := filepath.Join(t.TempDir(), "plugins")
	s, err := store.New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(i store.Installed, err error) (installedAPI, error) { return i, err }
	a := storeAPI{root: root}
	a.classify = func(err error) string {
		return classes(err, struct {
			name string
			err  error
		}{"exists", store.ErrExists}, struct {
			name string
			err  error
		}{"missing", store.ErrNotFound})
	}
	a.install = func(src string) (installedAPI, error) { return wrap(s.Install(src)) }
	a.upgrade = func(src string) (installedAPI, error) { return wrap(s.Upgrade(src)) }
	a.load = func(id string) (installedAPI, error) { return wrap(s.Load(id)) }
	a.remove = s.Remove
	a.list = func() ([]installedAPI, error) {
		items, err := s.List()
		var out []installedAPI
		for _, i := range items {
			out = append(out, i)
		}
		return out, err
	}
	a.verify = func() (string, error) {
		checks, err := s.Verify()
		var parts []string
		for _, c := range checks {
			parts = append(parts, c.ID+":"+a.classify(c.Err))
		}
		sort.Strings(parts)
		return strings.Join(parts, ","), err
	}
	return a
}

func newPureAPI(t *testing.T, limits cstore.Limits) storeAPI {
	root := filepath.Join(t.TempDir(), "plugins")
	s, err := cstore.New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(i cstore.Installed, err error) (installedAPI, error) { return i, err }
	a := storeAPI{root: root}
	a.classify = func(err error) string {
		return classes(err, struct {
			name string
			err  error
		}{"exists", cstore.ErrExists}, struct {
			name string
			err  error
		}{"missing", cstore.ErrNotFound})
	}
	a.install = func(src string) (installedAPI, error) { return wrap(s.Install(src)) }
	a.upgrade = func(src string) (installedAPI, error) { return wrap(s.Upgrade(src)) }
	a.load = func(id string) (installedAPI, error) { return wrap(s.Load(id)) }
	a.remove = s.Remove
	a.list = func() ([]installedAPI, error) {
		items, err := s.List()
		var out []installedAPI
		for _, i := range items {
			out = append(out, i)
		}
		return out, err
	}
	a.verify = func() (string, error) {
		checks, err := s.Verify()
		var parts []string
		for _, c := range checks {
			parts = append(parts, c.ID+":"+a.classify(c.Err))
		}
		sort.Strings(parts)
		return strings.Join(parts, ","), err
	}
	return a
}

// describe is everything observable about an installed package.
func describe(a storeAPI, i installedAPI, err error) string {
	if err != nil {
		return "err:" + a.classify(err)
	}
	digest, derr := i.Digest()
	art, aerr := i.ArtifactPath("main")
	exe, eerr := i.Executable()
	return fmt.Sprintf("%s@%s digest=%s/%s verify=%s artifact=%s/%s exe=%s/%s isexe=%v", i.Name(), i.Version(),
		digest, a.classify(derr), a.classify(i.Verify()), strings.TrimPrefix(art, a.root), a.classify(aerr),
		strings.TrimPrefix(exe, a.root), a.classify(eerr), i.IsExecutable())
}

func listDescribe(a storeAPI) string {
	items, err := a.list()
	if err != nil {
		return "list err:" + a.classify(err)
	}
	var out []string
	for _, i := range items {
		out = append(out, describe(a, i, nil))
	}
	return strings.Join(out, " | ")
}

func TestContractStoreMatchesEngineStore(t *testing.T) {
	type scenario struct {
		name string
		run  func(t *testing.T, a storeAPI) []string
	}
	scenarios := []scenario{
		{"source lifecycle", func(t *testing.T, a storeAPI) []string {
			var out []string
			i, err := a.install(source(t, "example/a", "r1", "print(1)"))
			out = append(out, describe(a, i, err))
			i, err = a.install(source(t, "example/a", "r1", "print(1)"))
			out = append(out, describe(a, i, err)) // already installed
			i, err = a.upgrade(source(t, "example/a", "r2", "print(2)"))
			out = append(out, describe(a, i, err))
			i, err = a.load("example/a")
			out = append(out, describe(a, i, err), listDescribe(a))
			i, err = a.load("example/missing")
			out = append(out, describe(a, i, err))
			out = append(out, a.classify(a.remove("example/a")), a.classify(a.remove("example/a")), listDescribe(a))
			return out
		}},
		{"tree artifacts", func(t *testing.T, a storeAPI) []string {
			i, err := a.install(treePackage(t, nil))
			out := []string{describe(a, i, err), listDescribe(a)}
			v, verr := a.verify()
			return append(out, v, a.classify(verr))
		}},
		{"tampered after install", func(t *testing.T, a storeAPI) []string {
			i, err := a.install(treePackage(t, nil))
			out := []string{describe(a, i, err)}
			dir := filepath.Join(a.root, "example%2ftree")
			if _, err := os.Stat(dir); err != nil {
				entries, _ := os.ReadDir(a.root)
				for _, e := range entries {
					dir = filepath.Join(a.root, e.Name())
				}
			}
			for _, mutate := range []func(){
				func() { _ = os.WriteFile(filepath.Join(dir, "vendor", "LICENSE"), []byte("changed"), 0o600) },
				func() { _ = os.WriteFile(filepath.Join(dir, "vendor", "extra.txt"), []byte("new"), 0o600) },
				func() { _ = os.Remove(filepath.Join(dir, "provider.py")) },
			} {
				mutate()
				l, lerr := a.load("example/tree")
				out = append(out, describe(a, l, lerr))
				v, verr := a.verify()
				out = append(out, v, a.classify(verr))
			}
			return out
		}},
		{"manifest name", func(t *testing.T, a storeAPI) []string {
			var out []string
			// the old name is not a manifest; package.json is an ordinary artifact
			old := source(t, "example/old", "r1", "print(1)")
			_ = os.Rename(filepath.Join(old, "plugin.json"), filepath.Join(old, "package.json"))
			i, err := a.install(old)
			out = append(out, describe(a, i, err))
			i, err = a.install(npmPackage(t, "example/npm"))
			out = append(out, describe(a, i, err))
			stray := source(t, "example/stray", "r1", "print(2)")
			_ = os.WriteFile(filepath.Join(stray, "package.json"), []byte("{}"), 0o600)
			i, err = a.install(stray)
			out = append(out, describe(a, i, err))
			i, err = a.install(treePackage(t, func(root string) {
				_ = os.WriteFile(filepath.Join(root, "vendor", "package.json"), []byte("{}"), 0o600)
			}))
			out = append(out, describe(a, i, err))
			v, verr := a.verify()
			return append(out, listDescribe(a), v, a.classify(verr))
		}},
		{"rejected sources", func(t *testing.T, a storeAPI) []string {
			var out []string
			bad := source(t, "example/b", "r1", "print(1)")
			_ = os.WriteFile(filepath.Join(bad, "src", "main.py"), []byte("tampered"), 0o600)
			i, err := a.install(bad)
			out = append(out, describe(a, i, err))
			linked := source(t, "example/c", "r1", "print(1)")
			_ = os.Remove(filepath.Join(linked, "src", "main.py"))
			_ = os.Symlink("/etc/hosts", filepath.Join(linked, "src", "main.py"))
			i, err = a.install(linked)
			out = append(out, describe(a, i, err))
			i, err = a.install(filepath.Join(t.TempDir(), "nope"))
			out = append(out, describe(a, i, err))
			i, err = a.install(t.TempDir())
			out = append(out, describe(a, i, err))
			return append(out, listDescribe(a))
		}},
		{"upgrade rules", func(t *testing.T, a storeAPI) []string {
			var out []string
			i, err := a.upgrade(source(t, "example/d", "r1", "print(1)"))
			out = append(out, describe(a, i, err)) // nothing to upgrade
			i, err = a.install(source(t, "example/d", "r1", "print(1)"))
			out = append(out, describe(a, i, err))
			i, err = a.upgrade(source(t, "example/d", "r1", "print(9)"))
			out = append(out, describe(a, i, err)) // same revision, different content
			return append(out, listDescribe(a))
		}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			engine := sc.run(t, newEngineAPI(t, store.Limits{}))
			pure := sc.run(t, newPureAPI(t, cstore.Limits{}))
			if len(engine) != len(pure) {
				t.Fatalf("step counts differ: engine %d, pure %d", len(engine), len(pure))
			}
			for i := range engine {
				// roots differ per run; compare with them removed
				if engine[i] != pure[i] {
					t.Errorf("step %d differs\n  engine: %s\n  pure:   %s", i, engine[i], pure[i])
				}
			}
		})
	}
}

// Limits are enforced identically.
func TestContractStoreLimitsMatchEngineStore(t *testing.T) {
	run := func(a storeAPI) []string {
		var out []string
		i, err := a.install(source(t, "example/big", "r1", strings.Repeat("x", 4096)))
		out = append(out, describe(a, i, err))
		i, err = a.install(treePackage(t, nil))
		out = append(out, describe(a, i, err))
		return out
	}
	engine := run(newEngineAPI(t, store.Limits{MaxArtifactBytes: 1024, MaxTreeFiles: 2, MaxTreeBytes: 8}))
	pure := run(newPureAPI(t, cstore.Limits{MaxArtifactBytes: 1024, MaxTreeFiles: 2, MaxTreeBytes: 8}))
	for i := range engine {
		if engine[i] != pure[i] {
			t.Errorf("limit step %d differs\n  engine: %s\n  pure:   %s", i, engine[i], pure[i])
		}
	}
}

// ListUnverified reads manifests only. The two stores must report the same
// packages, the same unreadable entries and the same error classes for the same
// bytes on disk, alone and layered.
func TestListUnverifiedMatchesContractStore(t *testing.T) {
	populate := func(t *testing.T, s *cstore.Store, tag string) {
		t.Helper()
		if _, err := s.Install(source(t, "example/a", "r"+tag, "one")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Install(treePackage(t, nil)); err != nil {
			t.Fatal(err)
		}
		broken, err := s.Install(source(t, "example/broken", "r1", "two"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(broken.Directory, cstore.ManifestName), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		moved, err := s.Install(source(t, "example/moved", "r1", "three"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(moved.Directory, filepath.Join(s.Root(), "example%2fmoved-elsewhere")); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"notes.txt"} {
			if err := os.WriteFile(filepath.Join(s.Root(), name), []byte("hi"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"Bad Name", ".staging-1"} {
			if err := os.Mkdir(filepath.Join(s.Root(), name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		// content that only a verifying call would notice
		tree := filepath.Join(s.Root(), "example%2ftree", "vendor", "LICENSE")
		if err := os.WriteFile(tree, []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	type result struct{ installed, problems []string }
	pureResult := func(root string, items []cstore.Installed, problems []cstore.Check) result {
		var r result
		for _, p := range items {
			r.installed = append(r.installed, fmt.Sprintf("%s|%s|%s|%s|%v", p.Manifest.Descriptor.Identity.ID, p.Name(), p.Version(), strings.TrimPrefix(p.Directory, root), p.IsExecutable()))
		}
		for _, c := range problems {
			r.problems = append(r.problems, fmt.Sprintf("%s|%s|%s", c.ID, strings.TrimPrefix(c.Directory, root), classes(c.Err)))
		}
		return r
	}
	engineResult := func(root string, items []store.Installed, problems []store.Check) result {
		var r result
		for _, p := range items {
			r.installed = append(r.installed, fmt.Sprintf("%s|%s|%s|%s|%v", p.Manifest.Descriptor.Identity.ID, p.Name(), p.Version(), strings.TrimPrefix(p.Directory, root), p.IsExecutable()))
		}
		for _, c := range problems {
			r.problems = append(r.problems, fmt.Sprintf("%s|%s|%s", c.ID, strings.TrimPrefix(c.Directory, root), classes(c.Err)))
		}
		return r
	}
	same := func(t *testing.T, label string, pure, engine result) {
		t.Helper()
		if strings.Join(pure.installed, "\n") != strings.Join(engine.installed, "\n") || strings.Join(pure.problems, "\n") != strings.Join(engine.problems, "\n") {
			t.Fatalf("%s differs\n pure:   %v / %v\n engine: %v / %v", label, pure.installed, pure.problems, engine.installed, engine.problems)
		}
		if len(pure.installed) == 0 || len(pure.problems) == 0 {
			t.Fatalf("%s: vacuous result %v / %v", label, pure.installed, pure.problems)
		}
	}

	user, system := filepath.Join(t.TempDir(), "user"), filepath.Join(t.TempDir(), "system")
	pureUser, err := cstore.New(user, cstore.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	pureSystem, err := cstore.New(system, cstore.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	populate(t, pureUser, "usr")
	populate(t, pureSystem, "sys")
	engineUser, err := store.New(user, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	engineSystem, err := store.New(system, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}

	pi, pp, perr := pureUser.ListUnverified()
	ei, ep, eerr := engineUser.ListUnverified()
	if perr != nil || eerr != nil {
		t.Fatal(perr, eerr)
	}
	same(t, "store", pureResult(user, pi, pp), engineResult(user, ei, ep))

	pureLayers, err := cstore.NewLayered(pureUser, pureSystem)
	if err != nil {
		t.Fatal(err)
	}
	engineLayers, err := store.NewLayered(engineUser, engineSystem)
	if err != nil {
		t.Fatal(err)
	}
	pli, plp, perr := pureLayers.ListUnverified()
	eli, elp, eerr := engineLayers.ListUnverified()
	if perr != nil || eerr != nil {
		t.Fatal(perr, eerr)
	}
	// directories differ by root, so compare with both roots removed
	strip := func(r result) result {
		for i := range r.installed {
			r.installed[i] = strings.NewReplacer(user, "", system, "").Replace(r.installed[i])
		}
		for i := range r.problems {
			r.problems[i] = strings.NewReplacer(user, "", system, "").Replace(r.problems[i])
		}
		return r
	}
	same(t, "layered", strip(pureResult("", pli, plp)), strip(engineResult("", eli, elp)))
}
