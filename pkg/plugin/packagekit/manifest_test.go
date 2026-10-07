package packagekit_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/packagekit"
)

func manifest(id string) packagekit.Manifest {
	sum := sha256.Sum256([]byte("payload"))
	return packagekit.Manifest{APIVersion: packagekit.Version, Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: id, Revision: "r1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: id, Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}}, Artifacts: []packagekit.Artifact{{Name: "source", Path: "source.txt", SHA256: hex.EncodeToString(sum[:])}}, Entrypoints: []packagekit.Entrypoint{{Name: "default", Runtime: "jsonline", Artifact: "source", Protocols: []string{plugin.APIVersion}}}}
}
func TestIntegrityAndSelection(t *testing.T) {
	m := manifest("example/a")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyArtifacts(root); err != nil {
		t.Fatal(err)
	}
	m.SharedDependencies = []packagekit.SharedDependency{{Name: "@example/ui", Versions: []string{"1.0"}}}
	env := packagekit.Environment{Runtimes: map[string]plugin.BackendProfile{"jsonline": jsonline.Profile()}}
	if _, err := m.SelectEntrypoint("default", env); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatal(err)
	}
	env.SharedVersions = map[string]string{"@example/ui": "1.0"}
	if _, err := m.SelectEntrypoint("default", env); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.VerifyArtifacts(root), plugin.ErrMismatch) {
		t.Fatal("digest mismatch accepted")
	}
	for _, path := range []string{"../escape", "/absolute", "a\\b", "a:stream", "a/../b", "a\n"} {
		m.Artifacts[0].Path = path
		if m.Validate() == nil {
			t.Fatal(path)
		}
	}
}
func TestResolutionAndGraph(t *testing.T) {
	a, b := manifest("example/a"), manifest("example/b")
	a.Requires = []packagekit.Dependency{{Contract: b.Descriptor.Contracts[0].ContractRef, Operation: "read"}}
	p, err := packagekit.Resolve([]packagekit.Manifest{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Order) != 2 || p.Order[0] != b.Descriptor.Identity {
		t.Fatal(p)
	}
	tx := p.Graph("plugins")
	if len(tx.Vertices) != 2 || len(tx.Edges) != 1 || tx.Edges[0].Type != "requires" {
		t.Fatal(tx)
	}
	b.Requires = []packagekit.Dependency{{Contract: a.Descriptor.Contracts[0].ContractRef, Operation: "read"}}
	if _, err := packagekit.Resolve([]packagekit.Manifest{a, b}); err == nil {
		t.Fatal("cycle accepted")
	}
	if _, err := packagekit.Resolve([]packagekit.Manifest{a}); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatal(err)
	}
}
