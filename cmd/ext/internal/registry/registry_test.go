package registry

import (
	"io"
	"testing"
)

func run(args []string, stdout, stderr io.Writer) int { return 0 }

func TestRegisterRefusesDuplicatesAndBadEntries(t *testing.T) {
	r := New()
	if err := r.Register(Command{Name: "ctx", Source: Builtin, Run: run}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Command{Name: "ctx", Source: Package, Run: run}); err == nil {
		t.Fatal("a second registration of ctx was accepted")
	}
	for _, bad := range []Command{{Source: Builtin, Run: run}, {Name: "x", Source: Builtin}, {Name: "y", Source: "other", Run: run}, {Name: "version", Source: Builtin, Run: run}} {
		if err := r.Register(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if c, ok := r.Lookup("ctx"); !ok || c.Source != Builtin {
		t.Fatalf("lookup = %+v, %v", c, ok)
	}
}

func TestListIsSortedAndClosestFindsTypos(t *testing.T) {
	r := New()
	for _, n := range []string{"ctn", "ctx", "git"} {
		if err := r.Register(Command{Name: n, Source: Builtin, Run: run}); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, c := range r.List() {
		names = append(names, c.Name)
	}
	if got := names[0] + names[1] + names[2]; got != "ctnctxgit" {
		t.Fatalf("order = %v", names)
	}
	if n, ok := r.Closest("cxt"); !ok || n != "ctx" {
		t.Fatalf("closest(cxt) = %q, %v", n, ok)
	}
	if n, ok := r.Closest("completely-different"); ok {
		t.Fatalf("closest = %q for an unrelated name", n)
	}
}
