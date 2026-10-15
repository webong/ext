package packagekit_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/jsonline"
	"github.com/webong/ext/pkg/plugin-go/packagekit"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestLicenseAndMetadataRoundTrip(t *testing.T) {
	m := manifest("example/a")
	m.License = "(Apache-2.0 OR MIT)"
	m.Metadata = map[string]json.RawMessage{
		"kvlite.module/v1": json.RawMessage(`{"driver":"rocksdb","symbol":"kvlite_module_init_v1"}`),
		"other.tool/v2":    json.RawMessage(` {"x":1} `),
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := packagekit.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.License != m.License || len(got.Metadata) != 2 {
		t.Fatal(got)
	}
	var kv struct{ Driver, Symbol string }
	if err := json.Unmarshal(got.Metadata["kvlite.module/v1"], &kv); err != nil || kv.Driver != "rocksdb" || kv.Symbol != "kvlite_module_init_v1" {
		t.Fatal(kv, err)
	}
}

func TestMetadataAndLicenseAreOptional(t *testing.T) {
	m := manifest("example/a")
	raw, _ := json.Marshal(m)
	if strings.Contains(string(raw), "license") || strings.Contains(string(raw), "metadata") {
		t.Fatal("unused fields must not be written:", string(raw))
	}
	if _, err := packagekit.Decode(raw); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsBadLicenseAndMetadata(t *testing.T) {
	for _, license := range []string{" MIT", "MIT ", "MIT  OR  Apache-2.0", "MIT;rm", "MIT\n", strings.Repeat("a", 129)} {
		m := manifest("example/a")
		m.License = license
		if !errors.Is(m.Validate(), plugin.ErrInvalid) {
			t.Fatalf("license %q accepted", license)
		}
	}
	object := json.RawMessage(`{}`)
	bad := map[string]json.RawMessage{
		"kvlite":           object,                 // not versioned
		"KVLite/v1":        object,                 // not lowercase
		"kvlite.module/v0": object,                 // version starts at 1
		"kvlite.module/v1": json.RawMessage(`[]`),  // not an object
		"x/v1":             json.RawMessage(`"s"`), // not an object
		"y/v1":             json.RawMessage(`null`),
		"z/v1":             json.RawMessage(``),
		"w/v1":             json.RawMessage(`{"unterminated":`), // built in code, never decoded
	}
	for key, value := range bad {
		m := manifest("example/a")
		m.Metadata = map[string]json.RawMessage{key: value}
		if !errors.Is(m.Validate(), plugin.ErrInvalid) {
			t.Fatalf("metadata %q=%s accepted", key, value)
		}
	}
	m := manifest("example/a")
	m.Metadata = map[string]json.RawMessage{"big/v1": json.RawMessage(`{"a":"` + strings.Repeat("x", 64<<10) + `"}`)}
	if !errors.Is(m.Validate(), plugin.ErrInvalid) {
		t.Fatal("oversize metadata value accepted")
	}
	m.Metadata = map[string]json.RawMessage{}
	for i := 0; i < 65; i++ {
		m.Metadata[fmt.Sprintf("k%d/v1", i)] = object
	}
	if !errors.Is(m.Validate(), plugin.ErrInvalid) {
		t.Fatal("too many metadata keys accepted")
	}
	m.Metadata = map[string]json.RawMessage{}
	for i := 0; i < 5; i++ {
		m.Metadata[fmt.Sprintf("k%d/v1", i)] = json.RawMessage(`{"a":"` + strings.Repeat("x", 60<<10) + `"}`)
	}
	if !errors.Is(m.Validate(), plugin.ErrInvalid) {
		t.Fatal("oversize total metadata accepted")
	}
}

func TestDecodeStillRejectsUnknownAndDuplicateFields(t *testing.T) {
	m := manifest("example/a")
	m.Metadata = map[string]json.RawMessage{"a.b/v1": json.RawMessage(`{"k":1}`)}
	raw, _ := json.Marshal(m)
	unknown := strings.Replace(string(raw), `"artifacts"`, `"surprise":1,"artifacts"`, 1)
	if _, err := packagekit.Decode([]byte(unknown)); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal("unknown top-level field accepted:", err)
	}
	dup := strings.Replace(string(raw), `{"k":1}`, `{"k":1,"k":2}`, 1)
	if _, err := packagekit.Decode([]byte(dup)); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal("duplicate key inside metadata accepted:", err)
	}
}
