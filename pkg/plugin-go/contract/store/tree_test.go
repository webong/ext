package store_test

import (
	"encoding/json"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/packagekit"
	"github.com/webong/ext/pkg/plugin-go/contract/store"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var treeContracts = []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.read", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}

// treePackage builds a source package with provider.py and a vendored tree whose
// files use upper-case and nested names.
func treePackage(t *testing.T, mutate func(root string)) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"provider.py":              "print(1)",
		"vendor/LICENSE":           "license",
		"vendor/pkg/METADATA":      "meta",
		"vendor/pkg/lib/native.so": "elf",
		"vendor/pkg/bin/run":       "#!/bin/sh\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if name == "vendor/pkg/bin/run" {
			mode = 0o755
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	if mutate != nil {
		mutate(root)
	}
	m, err := packagekit.SourceManifest("example/tree", "1.0.0", treeContracts, root, []string{"provider.py", "vendor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(root, m); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTreeArtifactLifecycle(t *testing.T) {
	s := newStore(t)
	src := treePackage(t, nil)
	p, err := s.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Manifest.Artifacts) != 2 || p.Manifest.Artifacts[1].Kind != packagekit.ArtifactTree {
		t.Fatalf("artifacts: %+v", p.Manifest.Artifacts)
	}
	tree, err := p.ArtifactPath("vendor")
	if err != nil || filepath.Base(tree) != "vendor" {
		t.Fatalf("tree path: %q %v", tree, err)
	}
	for _, name := range []string{"LICENSE", "pkg/METADATA", "pkg/lib/native.so"} {
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(tree, "pkg", "bin", "run")); info.Mode().Perm()&0o100 == 0 {
			t.Fatal("owner-execute bit was not kept")
		}
		if info, _ := os.Stat(filepath.Join(tree, "LICENSE")); info.Mode().Perm()&0o111 != 0 {
			t.Fatal("plain file became executable")
		}
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	// The package digest covers tree files, and List verifies them.
	d1, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	// Tampering inside the tree is detected.
	if err := os.WriteFile(filepath.Join(tree, "LICENSE"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := p.ArtifactPath("vendor"); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("tamper via ArtifactPath: %v", err)
	}
	os.WriteFile(filepath.Join(tree, "LICENSE"), []byte("license"), 0o600)
	if err := p.Verify(); err != nil {
		t.Fatalf("restored: %v", err)
	}
	// A file added to the tree also changes the digest.
	os.WriteFile(filepath.Join(tree, "extra"), []byte("x"), 0o600)
	if err := p.Verify(); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("added file: %v", err)
	}
	os.Remove(filepath.Join(tree, "extra"))
	// A stray file outside any artifact is still refused.
	os.WriteFile(filepath.Join(p.Directory, "stray"), []byte("x"), 0o600)
	if err := p.Verify(); err == nil {
		t.Fatal("unlisted file accepted")
	}
	os.Remove(filepath.Join(p.Directory, "stray"))
	if d2, _ := p.Digest(); d2 != d1 {
		t.Fatal("digest not stable")
	}
	// Upgrade replaces the tree.
	up := treePackage(t, func(root string) { os.WriteFile(filepath.Join(root, "vendor", "LICENSE"), []byte("v2"), 0o600) })
	raw, _ := os.ReadFile(filepath.Join(up, store.ManifestName))
	var m packagekit.Manifest
	json.Unmarshal(raw, &m)
	if m.Descriptor.Identity.Revision == p.Manifest.Descriptor.Identity.Revision {
		t.Fatal("tree change did not change the revision")
	}
	if _, err := s.Upgrade(up); err != nil {
		t.Fatal(err)
	}
}

func TestTreeInstallRejections(t *testing.T) {
	if runtime.GOOS != "windows" {
		src := treePackage(t, nil)
		os.Symlink(filepath.Join(src, "provider.py"), filepath.Join(src, "vendor", "link"))
		if _, err := newStore(t).Install(src); err == nil {
			t.Fatal("tree with a symlink installed")
		}
	}
	// Source changed after the manifest was written.
	src := treePackage(t, nil)
	os.WriteFile(filepath.Join(src, "vendor", "LICENSE"), []byte("tampered"), 0o600)
	if _, err := newStore(t).Install(src); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("tampered source: %v", err)
	}
	// Bounds.
	limited := func(l store.Limits) *store.Store {
		s, err := store.New(filepath.Join(t.TempDir(), "p"), l)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for name, l := range map[string]store.Limits{
		"too many files": {MaxTreeFiles: 3},
		"too many bytes": {MaxTreeBytes: 10},
		"file too large": {MaxArtifactBytes: 5},
	} {
		if _, err := limited(l).Install(treePackage(t, nil)); err == nil {
			t.Fatalf("%s: installed", name)
		}
	}
	if _, err := limited(store.Limits{MaxTreeFiles: 100, MaxTreeBytes: 1 << 20}).Install(treePackage(t, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.New("x", store.Limits{MaxTreeFiles: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
}
