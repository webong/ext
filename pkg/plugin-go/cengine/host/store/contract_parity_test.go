package store_test

import (
	"errors"
	"fmt"
	"github.com/webong/ext/pkg/plugin-go/cengine/host/store"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	cstore "github.com/webong/ext/pkg/plugin-go/contract/host/store"
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
