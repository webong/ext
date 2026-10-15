package goengine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/packagekit"
)

// These tests run in both builds, with and without the C engine, so the common
// API means the same thing everywhere.

func TestModelReexportsAreThePluginTypes(t *testing.T) {
	var d Descriptor = plugin.Descriptor{APIVersion: APIVersion}
	if d.APIVersion != plugin.APIVersion {
		t.Fatal("alias differs")
	}
	if !errors.Is(ErrInvalid, plugin.ErrInvalid) || !errors.Is(plugin.ErrDenied, ErrDenied) {
		t.Fatal("error classes must be shared values")
	}
	if DefaultTimeout != plugin.DefaultTimeout || MaxFrameBytes != plugin.MaxFrameBytes {
		t.Fatal("constants differ")
	}
}

func TestSHA256AndDigestsAgreeWithTheStandardLibrary(t *testing.T) {
	sum := SHA256([]byte("payload"))
	if want := sha256.Sum256([]byte("payload")); sum != want {
		t.Fatal(sum, want)
	}
	root := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("source.txt", "payload")
	put("vendor/a.txt", "one")
	put("vendor/lib/b.txt", "two")
	if err := os.MkdirAll(filepath.Join(root, "empty", "inner"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := ArtifactDigest(root, "source.txt", false)
	if want := hex.EncodeToString(sum[:]); err != nil || got != want {
		t.Fatalf("file digest %q (%v), want %q", got, err, want)
	}
	for _, dir := range []string{"vendor", "vendor/lib"} {
		want, err := packagekit.TreeDigest(root, dir)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := ArtifactDigest(root, dir, true); err != nil || got != want {
			t.Errorf("tree %s: %q (%v), want %q", dir, got, err, want)
		}
	}
	if d, err := DirectoryDigest(root); err != nil || d == "" {
		t.Fatal(d, err)
	}
	for _, bad := range []struct {
		path string
		tree bool
	}{{"empty", true}, {"missing", true}, {"source.txt", true}, {"vendor", false}, {"missing.txt", false}, {"../escape", false}, {"/abs", true}, {"a\\b", false}, {"", true}, {"CON", false}} {
		if _, err := ArtifactDigest(root, bad.path, bad.tree); err == nil {
			t.Errorf("ArtifactDigest(%q, tree=%v) accepted", bad.path, bad.tree)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "source.txt"), filepath.Join(root, "link.txt")); err == nil {
			if _, err := ArtifactDigest(root, "link.txt", false); !errors.Is(err, plugin.ErrInvalid) {
				t.Errorf("symbolic link accepted: %v", err)
			}
		}
	}
}
