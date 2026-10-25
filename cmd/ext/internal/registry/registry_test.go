package registry

import (
	"errors"
	"io"
	"testing"
)

func run(args []string, stdout, stderr io.Writer) int { return 0 }

func mustRegister(t *testing.T, r *Registry, name string, source Source) {
	t.Helper()
	if err := r.Register(Command{Name: name, Source: source, Version: string(source), Run: run}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterRefusesDuplicatesAndBadEntries(t *testing.T) {
	r := New()
	mustRegister(t, r, "ctx", Builtin)
	if err := r.Register(Command{Name: "ctx", Source: Builtin, Run: run}); err == nil {
		t.Fatal("a second builtin ctx was accepted")
	}
	mustRegister(t, r, "ctx", Package) // a package beside the builtin is allowed
	for _, bad := range []Command{{Source: Builtin, Run: run}, {Name: "x", Source: Builtin}, {Name: "y", Source: "other", Run: run}, {Name: "version", Source: Builtin, Run: run}} {
		if err := r.Register(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestResolvePrefersBuiltinThenPinThenFlag(t *testing.T) {
	r := New()
	mustRegister(t, r, "ctx", Builtin)
	mustRegister(t, r, "ctx", Package)
	mustRegister(t, r, "ctn", Builtin)
	mustRegister(t, r, "tool", Package)

	if c, why, err := r.Resolve("ctx", ""); err != nil || c.Source != Builtin || why != ByDefault {
		t.Fatalf("default = %+v %s %v", c, why, err)
	}
	if c, _, err := r.Resolve("tool", ""); err != nil || c.Source != Package {
		t.Fatalf("package-only = %+v %v", c, err)
	}
	if err := r.Pin("ctx", Package); err != nil {
		t.Fatal(err)
	}
	if c, why, _ := r.Resolve("ctx", ""); c.Source != Package || why != ByPin {
		t.Fatalf("pinned = %+v %s", c, why)
	}
	if c, _, _ := r.Resolve("ctn", ""); c.Source != Builtin {
		t.Fatal("a pin on ctx changed ctn")
	}
	if c, why, _ := r.Resolve("ctx", Builtin); c.Source != Builtin || why != ByFlag {
		t.Fatalf("flag over pin = %+v %s", c, why)
	}
}

func TestResolveNeverFallsBack(t *testing.T) {
	r := New()
	mustRegister(t, r, "ctn", Builtin)
	if _, _, err := r.Resolve("ctn", Package); err == nil {
		t.Fatal("--source=package resolved a builtin-only command")
	}
	if err := r.Pin("ctn", Package); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Resolve("ctn", ""); err == nil {
		t.Fatal("a pin to a missing source fell back")
	}
	if _, _, err := r.Resolve("none", ""); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown = %v", err)
	}
	if _, _, err := r.Resolve("ctn", "other"); err == nil {
		t.Fatal("accepted an unknown source")
	}
	if err := r.Pin("ctn", "other"); err == nil {
		t.Fatal("pinned an unknown source")
	}
}

func TestListMarksTheActiveEntryAndClosestFindsTypos(t *testing.T) {
	r := New()
	mustRegister(t, r, "ctx", Package)
	mustRegister(t, r, "ctx", Builtin)
	mustRegister(t, r, "ctn", Builtin)
	got := ""
	for _, e := range r.List() {
		mark := "-"
		if e.Active {
			mark = "+"
		}
		got += e.Name + ":" + string(e.Source) + mark + " "
	}
	if want := "ctn:builtin+ ctx:builtin+ ctx:package- "; got != want {
		t.Fatalf("list = %q, want %q", got, want)
	}
	if n, ok := r.Closest("cxt"); !ok || n != "ctx" {
		t.Fatalf("closest(cxt) = %q, %v", n, ok)
	}
	if n, ok := r.Closest("completely-different"); ok {
		t.Fatalf("closest = %q for an unrelated name", n)
	}
}
