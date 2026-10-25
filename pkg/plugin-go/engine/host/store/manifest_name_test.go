package store_test

import (
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/host/store"
	"github.com/webong/ext/pkg/plugin-go/engine/package"
	"os"
	"path/filepath"
	"testing"
)

// The manifest is plugin.json and nothing else. package.json is the name npm
// and Node tooling use, so a package that carries a Node tree, or ships an npm
// package.json as one of its files, must be able to.

func TestManifestIsPluginJSON(t *testing.T) {
	if store.ManifestName != "plugin.json" {
		t.Fatalf("manifest name %q", store.ManifestName)
	}
	src := source(t, "example/a", "r1", "one")
	if _, err := os.Stat(filepath.Join(src, "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "package.json")); !os.IsNotExist(err) {
		t.Fatalf("package.json written beside the manifest: %v", err)
	}
}

func TestPackageJSONIsNotReadAsAManifest(t *testing.T) {
	s := newStore(t)

	// a source whose manifest still has the old name
	old := source(t, "example/old", "r1", "one")
	if err := os.Rename(filepath.Join(old, "plugin.json"), filepath.Join(old, "package.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Install: %v", err)
	}
	if _, err := s.Load("example/old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("nothing was installed: %v", err)
	}

	// an installed package whose manifest has the old name
	installed, err := s.Install(source(t, "example/old", "r1", "one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(source(t, "example/fine", "r1", "two")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(installed.Directory, "plugin.json"), filepath.Join(installed.Directory, "package.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("example/old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load: %v", err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List accepted a package without a plugin.json")
	}
	checks, err := s.Verify()
	if err != nil {
		t.Fatal(err)
	}
	var reported bool
	for _, c := range checks {
		if c.ID == "example/old" {
			reported = errors.Is(c.Err, os.ErrNotExist)
		} else if c.Err != nil {
			t.Fatalf("%s: %v", c.ID, c.Err)
		}
	}
	if !reported {
		t.Fatalf("Verify did not report the missing manifest: %+v", checks)
	}
	listed, problems, err := s.ListUnverified()
	if err != nil || len(listed) != 1 || listed[0].Name() != "example/fine" || len(problems) != 1 || problems[0].ID != "example/old" || !errors.Is(problems[0].Err, os.ErrNotExist) {
		t.Fatalf("ListUnverified: %v %+v %v", listed, problems, err)
	}
}

func TestTreeMayHoldItsOwnManifestNames(t *testing.T) {
	s := newStore(t)
	src := treePackage(t, func(root string) {
		for name, body := range map[string]string{
			"vendor/package.json":     `{"name":"left-pad","version":"1.0.0"}`,
			"vendor/pkg/package.json": `{"name":"inner"}`,
			"vendor/plugin.json":      `{"someone":"else's manifest"}`,
		} {
			write(t, root, name, body)
		}
	})
	p, err := s.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ArtifactPath("vendor"); err != nil {
		t.Fatal(err)
	}
	// the files are part of the tree's digest: changing one is a mismatch
	for _, name := range []string{"vendor/package.json", "vendor/pkg/package.json", "vendor/plugin.json"} {
		path := filepath.Join(p.Directory, filepath.FromSlash(name))
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := p.Verify(); !errors.Is(err, plugin.ErrMismatch) {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(path, before, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
}

// npmPackage builds a package that ships a top-level package.json as one of its
// artifacts, beside the plugin.json manifest.
func npmPackage(t *testing.T, id string) string {
	t.Helper()
	dir := t.TempDir()
	main := write(t, dir, "src/main.py", "print(1)")
	npm := write(t, dir, "package.json", `{"name":"payload","dependencies":{}}`)
	m := packagekit.Manifest{APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: id, Revision: "r1"},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.read", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}},
		Artifacts: []packagekit.Artifact{{Name: "main", Path: "src/main.py", SHA256: main}, {Name: "npm", Path: "package.json", SHA256: npm}}}
	if err := store.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTopLevelPackageJSONAsADeclaredArtifact(t *testing.T) {
	s := newStore(t)
	p, err := s.Install(npmPackage(t, "example/npm"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	path, err := p.ArtifactPath("npm")
	if err != nil || filepath.Base(path) != "package.json" || filepath.Dir(path) != p.Directory {
		t.Fatalf("%q %v", path, err)
	}
	if _, err := s.Load("example/npm"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"name":"evil"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("a changed artifact verified: %v", err)
	}
	if _, err := p.ArtifactPath("npm"); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("a changed artifact was served: %v", err)
	}
}

func TestStrayTopLevelPackageJSONIsUnlisted(t *testing.T) {
	s := newStore(t)
	p, err := s.Install(source(t, "example/stray", "r1", "one"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Directory, "package.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(); err == nil {
		t.Fatal("an unlisted package.json verified")
	}
	if _, err := s.Load("example/stray"); err == nil {
		t.Fatal("Load accepted an unlisted package.json")
	}
	// the same file in a source is refused at install
	src := source(t, "example/stray2", "r1", "two")
	if err := os.WriteFile(filepath.Join(src, "package.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(src); err == nil {
		t.Fatal("Install accepted an unlisted package.json")
	}
}

func TestManifestPathIsReservedInAnyCase(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"plugin.json", "PLUGIN.JSON", "Plugin.Json"} {
		src := source(t, "example/reserved", "r1", "one")
		raw, err := os.ReadFile(filepath.Join(src, "plugin.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m packagekit.Manifest
		if err := plugin.Decode(raw, &m); err != nil {
			t.Fatal(err)
		}
		m.Artifacts = append(m.Artifacts, packagekit.Artifact{Name: "clash", Path: name, SHA256: m.Artifacts[0].SHA256})
		if err := store.WriteManifest(src, m); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Install(src); !errors.Is(err, plugin.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
