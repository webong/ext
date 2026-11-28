package dirlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writer")
	release, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); err == nil {
		t.Fatal("acquired held lock")
	}
	// Preserve the old directory so inode reuse cannot mask replacement.
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	other, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err == nil {
		t.Fatal("released another owner's directory")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := other(); err != nil {
		t.Fatal(err)
	}
	if err := other(); err != nil {
		t.Fatal("release is not idempotent", err)
	}
	next, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := next(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseAfterWorkingDirectoryChange(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	release, err := Acquire(filepath.Join("locks", "writer"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "locks", "writer")); !os.IsNotExist(err) {
		t.Fatalf("lock remains: %v", err)
	}
}
