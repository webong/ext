package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/packagekit"
	"github.com/webong/ext/pkg/plugin/store"
)

func write(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// source builds a source-only package (no entrypoints).
func source(t *testing.T, id, revision, body string) string {
	t.Helper()
	dir := t.TempDir()
	d := write(t, dir, "src/main.py", body)
	m := packagekit.Manifest{APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: id, Revision: revision},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.read", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}},
		Artifacts: []packagekit.Artifact{{Name: "main", Path: "src/main.py", SHA256: d}}}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, store.ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newStore(t *testing.T) *store.Store {
	s, err := store.New(filepath.Join(t.TempDir(), "plugins"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLifecycle(t *testing.T) {
	s := newStore(t)
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	p, err := s.Install(source(t, "example/a", "r1", "one"))
	if err != nil || !p.Manifest.SourceOnly() {
		t.Fatal(p, err)
	}
	if _, err := s.Install(source(t, "example/a", "r2", "two")); !errors.Is(err, store.ErrExists) {
		t.Fatal(err)
	}
	path, err := p.ArtifactPath("main")
	if err != nil || filepath.Base(path) != "main.py" {
		t.Fatal(path, err)
	}
	d1, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upgrade(source(t, "example/a", "r1", "two")); err == nil {
		t.Fatal("same revision upgrade accepted")
	}
	up, err := s.Upgrade(source(t, "example/a", "r2", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if d2, _ := up.Digest(); d2 == d1 {
		t.Fatal("digest unchanged")
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Manifest.Descriptor.Identity.Revision != "r2" {
		t.Fatal(list, err)
	}
	if err := s.Remove("example/a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("example/a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.Root())
	if len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestUpgradeRejectsContractChange(t *testing.T) {
	s := newStore(t)
	if _, err := s.Install(source(t, "example/a", "r1", "one")); err != nil {
		t.Fatal(err)
	}
	other := source(t, "example/a", "r2", "two")
	raw, _ := os.ReadFile(filepath.Join(other, store.ManifestName))
	raw = []byte(string(raw[:0]) + replace(string(raw), "example.read", "example.write"))
	os.WriteFile(filepath.Join(other, store.ManifestName), raw, 0o600)
	if _, err := s.Upgrade(other); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal(err)
	}
	if p, err := s.Load("example/a"); err != nil || p.Manifest.Descriptor.Identity.Revision != "r1" {
		t.Fatal("previous install damaged", err)
	}
}

func replace(s, a, b string) string {
	for i := 0; i+len(a) <= len(s); i++ {
		if s[i:i+len(a)] == a {
			return s[:i] + b + s[i+len(a):]
		}
	}
	return s
}

func TestRejectsTamperedSource(t *testing.T) {
	s := newStore(t)
	src := source(t, "example/a", "r1", "one")
	write(t, src, "src/main.py", "tampered")
	if _, err := s.Install(src); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatal("partial install visible")
	}
}

func TestRejectsSymlinkAndOversize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	s := newStore(t)
	src := source(t, "example/a", "r1", "one")
	target := filepath.Join(t.TempDir(), "real")
	os.WriteFile(target, []byte("one"), 0o600)
	os.Remove(filepath.Join(src, "src/main.py"))
	os.Symlink(target, filepath.Join(src, "src/main.py"))
	if _, err := s.Install(src); err == nil {
		t.Fatal("symlink artifact installed")
	}
	small, _ := store.New(filepath.Join(t.TempDir(), "p"), store.Limits{MaxArtifactBytes: 2})
	if _, err := small.Install(source(t, "example/b", "r1", "three")); err == nil {
		t.Fatal("oversize artifact installed")
	}
}

func TestVerifyDetectsPostInstallChange(t *testing.T) {
	s := newStore(t)
	p, err := s.Install(source(t, "example/a", "r1", "one"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, p.Directory, "extra.txt", "x")
	if err := p.Verify(); err == nil {
		t.Fatal("unlisted file accepted")
	}
	if _, err := s.List(); err == nil {
		t.Fatal("list must fail closed")
	}
	os.Remove(filepath.Join(p.Directory, "extra.txt"))
	write(t, p.Directory, "src/main.py", "evil")
	if _, err := p.ArtifactPath("main"); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal(err)
	}
}

func TestReservedPathAndEmptyRoot(t *testing.T) {
	if _, err := store.New("", store.Limits{}); err == nil {
		t.Fatal("empty root accepted")
	}
	s := newStore(t)
	src := t.TempDir()
	d := write(t, src, store.ManifestName+"x", "x")
	_ = d
	m := packagekit.Manifest{APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/r", Revision: "r1"},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "c", Version: "v1"}, Operations: []plugin.Operation{{Name: "o"}}}}},
		Artifacts: []packagekit.Artifact{{Name: "m", Path: store.ManifestName, SHA256: d}}}
	raw, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(src, store.ManifestName), raw, 0o600)
	if _, err := s.Install(src); err == nil {
		t.Fatal("reserved artifact path accepted")
	}
}
