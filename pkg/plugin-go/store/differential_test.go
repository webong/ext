//go:build ext_cengine && cgo && (darwin || linux || windows)

package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin-go/store"
	gostore "github.com/webong/ext/pkg/plugin/store"
)

func sameClass(t *testing.T, label string, c, g error) {
	t.Helper()
	for _, class := range []error{nil, plugin.ErrInvalid, plugin.ErrMismatch, plugin.ErrNotFound, plugin.ErrUnsupported, store.ErrExists, store.ErrNotFound} {
		if errors.Is(c, class) != errors.Is(g, class) {
			t.Errorf("%s: C-backed %v, Go %v", label, c, g)
			return
		}
	}
	if (c == nil) != (g == nil) {
		t.Errorf("%s: C-backed %v, Go %v", label, c, g)
	}
}

// TestMatchesGoStore puts the same packages through the Go store and the
// C-backed store and requires the same digests and the same verdicts, before and
// after each kind of damage.
func TestMatchesGoStore(t *testing.T) {
	cs := newStore(t)
	gs, err := gostore.New(filepath.Join(t.TempDir(), "plugins"), gostore.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		"example/a": source(t, "example/a", "r1", "one"),
		"example/b": source(t, "example/b", "r1", "two"),
		"example/c": source(t, "example/c", "r1", "three"),
	}
	for id, src := range sources {
		cp, cErr := cs.Install(src)
		gp, gErr := gs.Install(src)
		sameClass(t, "install "+id, cErr, gErr)
		cd, e1 := cp.Digest()
		gd, e2 := gp.Digest()
		if e1 != nil || e2 != nil {
			t.Fatal(e1, e2)
		}
		// The digest covers the stored manifest, which carries the same bytes.
		if cd == "" || gd == "" {
			t.Fatal("empty digest")
		}
		if _, err := cs.Install(src); !errors.Is(err, store.ErrExists) {
			t.Errorf("reinstall: %v", err)
		}
		if _, err := gs.Install(src); !errors.Is(err, gostore.ErrExists) {
			t.Errorf("Go reinstall: %v", err)
		}
	}
	cl, cErr := cs.List()
	gl, gErr := gs.List()
	sameClass(t, "list", cErr, gErr)
	if len(cl) != len(gl) {
		t.Fatal(len(cl), len(gl))
	}
	for i := range cl {
		if cl[i].Name() != gl[i].Name() {
			t.Fatalf("order: %s vs %s", cl[i].Name(), gl[i].Name())
		}
		cd, _ := cl[i].Digest()
		gd, _ := gl[i].Digest()
		if cd != gd {
			t.Errorf("%s digest: C-backed %s, Go %s", cl[i].Name(), cd, gd)
		}
	}

	damage := []struct {
		id   string
		harm func(dir string)
	}{
		{"example/a", func(dir string) { write(t, dir, "src/main.py", "evil") }},
		{"example/b", func(dir string) { write(t, dir, "extra.txt", "x") }},
		{"example/c", func(dir string) { os.Remove(filepath.Join(dir, "src/main.py")) }},
	}
	for _, d := range damage {
		cp, _ := cs.Load(d.id)
		gp, _ := gs.Load(d.id)
		d.harm(cp.Directory)
		d.harm(gp.Directory)
		_, cErr := cs.Load(d.id)
		_, gErr := gs.Load(d.id)
		sameClass(t, "load damaged "+d.id, cErr, gErr)
		if cErr == nil {
			t.Errorf("damage to %s went undetected", d.id)
		}
		_, cErr = cp.ArtifactPath("main")
		_, gErr = gp.ArtifactPath("main")
		sameClass(t, "artifact path "+d.id, cErr, gErr)
	}
	cc, cErr := cs.Verify()
	gc, gErr := gs.Verify()
	sameClass(t, "verify", cErr, gErr)
	if len(cc) != len(gc) {
		t.Fatal(len(cc), len(gc))
	}
	for i := range cc {
		if cc[i].ID != gc[i].ID {
			t.Fatalf("verify order %s vs %s", cc[i].ID, gc[i].ID)
		}
		sameClass(t, "verify "+cc[i].ID, cc[i].Err, gc[i].Err)
	}
}
