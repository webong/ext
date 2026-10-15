//go:build ext_cengine && cgo && windows

package goengine

import (
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A junction is a reparse point that is not a symbolic link, and needs no
// privilege to create. Package integrity must refuse it like a link.
func TestJunctionsAreRefused(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(target, "lib"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "a.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "lib", "b.txt"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "junction"), target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ArtifactDigest(root, "plain.txt", false); err != nil {
		t.Fatalf("plain file: %v", err)
	}
	for _, c := range []struct {
		path string
		tree bool
	}{{"junction", true}, {"junction/lib", true}, {"junction/a.txt", false}} {
		if _, err := ArtifactDigest(root, c.path, c.tree); !errors.Is(err, plugin.ErrInvalid) {
			t.Errorf("%s (tree=%v) through a junction: %v, want invalid", c.path, c.tree, err)
		}
	}
	// A junction inside a tree being walked is refused too.
	inner := t.TempDir()
	if err := os.MkdirAll(filepath.Join(inner, "vendor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "vendor", "ok.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(inner, "vendor", "linked"), target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	if _, err := ArtifactDigest(inner, "vendor", true); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("tree holding a junction: %v, want invalid", err)
	}
	// The root itself must not be a link.
	if _, err := DirectoryDigest(filepath.Join(root, "junction")); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("junction as root: %v, want invalid", err)
	}
}
