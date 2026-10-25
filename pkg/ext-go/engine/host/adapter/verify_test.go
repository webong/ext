package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func checkByName(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check for %s in %v", name, checks)
	return Check{}
}

func TestVerifyReportsTrustStates(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(root, "installed"))
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if _, err := store.Install(fixtureAdapter(t, filepath.Join(root, "source"), name, "computer")); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"alpha", "bravo"} {
		loaded, err := store.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Trust(loaded); err != nil {
			t.Fatal(err)
		}
	}
	bravo, _ := store.Load("bravo")
	if err := os.WriteFile(bravo.ExecutablePath(), []byte("changed\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	checks, err := store.Verify()
	if err != nil || len(checks) != 3 {
		t.Fatal(checks, err)
	}
	if c := checkByName(t, checks, "alpha"); !c.OK() || c.State != TrustTrusted {
		t.Fatal("alpha:", c)
	}
	if c := checkByName(t, checks, "bravo"); c.OK() || c.State != TrustChanged || c.Err != nil {
		t.Fatal("bravo must be reported changed:", c)
	}
	if c := checkByName(t, checks, "charlie"); c.OK() || c.State != TrustUntrusted || c.Err != nil {
		t.Fatal("charlie must be reported untrusted:", c)
	}
	for i := 1; i < len(checks); i++ {
		if checks[i-1].Name >= checks[i].Name {
			t.Fatal("not sorted", checks)
		}
	}
}

func TestVerifyReportsDamagedAdapterInsteadOfSkipping(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(root, "installed"))
	if _, err := store.Install(fixtureAdapter(t, filepath.Join(root, "source"), "alpha", "computer")); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(store.Home, "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "plugin.json"), []byte("not a manifest"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.Home, ".staging"), 0o755); err != nil {
		t.Fatal(err)
	}

	// List silently drops the broken adapter; Verify must surface it.
	if listed, _ := store.List(); len(listed) != 1 {
		t.Fatal("List behavior changed:", listed)
	}
	checks, err := store.Verify()
	if err != nil || len(checks) != 2 {
		t.Fatal(checks, err)
	}
	if c := checkByName(t, checks, "broken"); c.Err == nil || c.OK() || c.State != "" {
		t.Fatal("broken adapter not reported:", c)
	}
}

func TestVerifyNamedAndMissing(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(root, "installed"))
	if _, err := store.Install(fixtureAdapter(t, filepath.Join(root, "source"), "alpha", "computer")); err != nil {
		t.Fatal(err)
	}
	checks, err := store.Verify("alpha", "missing", "alpha")
	if err != nil || len(checks) != 2 {
		t.Fatal("duplicate names must be checked once:", checks, err)
	}
	if c := checkByName(t, checks, "missing"); c.Err == nil || c.OK() {
		t.Fatal("missing adapter not reported:", c)
	}
	if c := checkByName(t, checks, "alpha"); c.State != TrustUntrusted {
		t.Fatal(c)
	}
	if _, err := store.Verify("../escape"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyEmptyHomeAndUnreadableTrust(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "never-created"))
	if checks, err := store.Verify(); err != nil || len(checks) != 0 {
		t.Fatal(checks, err)
	}
	if _, err := os.Stat(store.Home); !os.IsNotExist(err) {
		t.Fatal("Verify created the home directory")
	}
	// A trust path that is a directory cannot be read: nothing may be called trusted.
	root := t.TempDir()
	store = NewStore(filepath.Join(root, "installed"))
	if _, err := store.Install(fixtureAdapter(t, filepath.Join(root, "source"), "alpha", "computer")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.TrustFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(); err == nil {
		t.Fatal("unreadable trust file must be an error")
	}
}
