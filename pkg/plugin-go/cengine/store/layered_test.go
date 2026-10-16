package store_test

import (
	"errors"
	"github.com/webong/ext/pkg/plugin-go/cengine/store"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func layered(t *testing.T, stores ...*store.Store) *store.Layered {
	t.Helper()
	l, err := store.NewLayered(stores...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLayeredUserShadowsSystem(t *testing.T) {
	user, system := newStore(t), newStore(t)
	if _, err := system.Install(source(t, "example/a", "sys", "system")); err != nil {
		t.Fatal(err)
	}
	if _, err := system.Install(source(t, "example/only-system", "sys", "s")); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Install(source(t, "example/a", "usr", "user")); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Install(source(t, "example/only-user", "usr", "u")); err != nil {
		t.Fatal(err)
	}
	l := layered(t, user, system)

	p, err := l.Load("example/a")
	if err != nil || p.Manifest.Descriptor.Identity.Revision != "usr" || !strings.HasPrefix(p.Directory, user.Root()) {
		t.Fatal("user copy must win", p, err)
	}
	if p, err := l.Load("example/only-system"); err != nil || !strings.HasPrefix(p.Directory, system.Root()) {
		t.Fatal("system-only package must be served", p, err)
	}
	if _, err := l.Load("example/missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := l.Load("Bad ID"); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}

	list, err := l.List()
	if err != nil || len(list) != 3 {
		t.Fatal(list, err)
	}
	got := map[string]string{}
	for _, p := range list {
		got[p.Name()] = p.Manifest.Descriptor.Identity.Revision
	}
	want := map[string]string{"example/a": "usr", "example/only-system": "sys", "example/only-user": "usr"}
	for id, rev := range want {
		if got[id] != rev {
			t.Fatalf("%s: got %q want %q (%v)", id, got[id], rev, got)
		}
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Name() >= list[i].Name() {
			t.Fatal("list not sorted by ID", list)
		}
	}
}

func TestLayeredFailsClosedOnDamagedWinner(t *testing.T) {
	user, system := newStore(t), newStore(t)
	if _, err := system.Install(source(t, "example/a", "sys", "system")); err != nil {
		t.Fatal(err)
	}
	p, err := user.Install(source(t, "example/a", "usr", "user"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, p.Directory, "src/main.py", "evil")
	l := layered(t, user, system)
	if _, err := l.Load("example/a"); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal("damaged override must not fall back to the system copy:", err)
	}
	if _, err := l.List(); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal("list must fail closed:", err)
	}
}

func TestLayeredNeverReadsShadowedCopy(t *testing.T) {
	user, system := newStore(t), newStore(t)
	if _, err := user.Install(source(t, "example/a", "usr", "user")); err != nil {
		t.Fatal(err)
	}
	s, err := system.Install(source(t, "example/a", "sys", "system"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, s.Directory, "src/main.py", "evil")
	l := layered(t, user, system)
	if p, err := l.Load("example/a"); err != nil || p.Manifest.Descriptor.Identity.Revision != "usr" {
		t.Fatal(p, err)
	}
	if list, err := l.List(); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
}

func TestLayeredDoesNotCreateRoots(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "root")
	system, err := store.New(missing, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	user := newStore(t)
	if _, err := user.Install(source(t, "example/a", "usr", "user")); err != nil {
		t.Fatal(err)
	}
	l := layered(t, user, system)
	if list, err := l.List(); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if _, err := l.Load("example/other"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("a read-only root was created")
	}
}

func TestLayeredRejectsBadConstruction(t *testing.T) {
	s := newStore(t)
	if _, err := store.NewLayered(); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := store.NewLayered(s, nil); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := store.NewLayered(s, s); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal("repeated root accepted:", err)
	}
	roots := layered(t, s).Roots()
	if len(roots) != 1 || roots[0] != s.Root() {
		t.Fatal(roots)
	}
}

func TestLayeredListFailsOnUnexpectedEntry(t *testing.T) {
	user, system := newStore(t), newStore(t)
	if _, err := system.Install(source(t, "example/a", "sys", "s")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user.Root(), "stray"), nil, 0o600); err != nil {
		if err := os.MkdirAll(user.Root(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(user.Root(), "stray"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := layered(t, user, system).List(); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestLayeredVerifyReportsWhatWouldBeServed(t *testing.T) {
	user, system := newStore(t), newStore(t)
	shadowed, err := system.Install(source(t, "example/a", "sys", "system"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, shadowed.Directory, "src/main.py", "evil") // shadowed, so never checked
	sysBad, err := system.Install(source(t, "example/b", "sys", "two"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, sysBad.Directory, "src/main.py", "evil")
	if _, err := system.Install(source(t, "example/c", "sys", "three")); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Install(source(t, "example/a", "usr", "user")); err != nil {
		t.Fatal(err)
	}
	userBad, err := user.Install(source(t, "example/d", "usr", "four"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, userBad.Directory, "src/main.py", "evil")

	checks, err := layered(t, user, system).Verify()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]error{}
	for _, c := range checks {
		if _, dup := got[c.ID]; dup {
			t.Fatalf("%s reported twice: %v", c.ID, checks)
		}
		got[c.ID] = c.Err
	}
	if len(got) != 4 {
		t.Fatal(checks)
	}
	if got["example/a"] != nil || got["example/c"] != nil {
		t.Fatal("served packages must verify; shadowed copy must be ignored:", got)
	}
	if !errors.Is(got["example/b"], plugin.ErrMismatch) || !errors.Is(got["example/d"], plugin.ErrMismatch) {
		t.Fatal("damaged packages must be reported in both stores:", got)
	}
}
