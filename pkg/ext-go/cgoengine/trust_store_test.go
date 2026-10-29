package cgoengine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cgo "github.com/webong/ext/pkg/ext-go/cgoengine"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/host/trust"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

func TestInstalledStoreDigestMatchesGo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	source := t.TempDir()
	body := []byte("one")
	if err := os.WriteFile(filepath.Join(source, "main"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	manifest := packagekit.Manifest{
		APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion,
			Identity:  plugin.Identity{ID: "example/a", Revision: "r1"},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.read", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}},
		Artifacts: []packagekit.Artifact{{Name: "main", Path: "main", SHA256: hex.EncodeToString(sum[:])}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(root, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	want, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	got, err := cgo.StoreDigest(root, "example/a")
	if err != nil || got != want {
		t.Fatalf("C/pure Go digest = %q, %v; want %q", got, err, want)
	}
	if err := os.WriteFile(filepath.Join(installed.Directory, "extra"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = cgo.StoreDigest(root, "example/a")
	if !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("unlisted file accepted: %v", err)
	}
}
func TestScopedTrustInteroperatesWithGoReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	first := sha256.Sum256([]byte("first"))
	second := sha256.Sum256([]byte("second"))
	a, b := hex.EncodeToString(first[:]), hex.EncodeToString(second[:])
	ref, err := trust.New(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cgo.TrustStateOf(path, "ext.command/v1", "example/a", a)
	if err != nil || state != trust.Untrusted {
		t.Fatalf("initial state = %v, %v", state, err)
	}
	if err := ref.Record("ext.command/v1", "example/a", a); err != nil {
		t.Fatal(err)
	}
	state, err = cgo.TrustStateOf(path, "ext.command/v1", "example/a", a)
	if err != nil || state != trust.Trusted {
		t.Fatalf("Go record in C = %v, %v", state, err)
	}
	state, err = cgo.TrustStateOf(path, "ext.command/v1", "example/a", b)
	if err != nil || state != trust.Changed {
		t.Fatalf("changed digest = %v, %v", state, err)
	}
	if err := cgo.TrustRecord(path, "inference.provider/v1", "example/a", b); err != nil {
		t.Fatal(err)
	}
	state, err = ref.StateOf("inference.provider/v1", "example/a", b)
	if err != nil || state != trust.Trusted {
		t.Fatalf("C record in Go = %v, %v", state, err)
	}
	if err := cgo.TrustRevoke(path, "ext.command/v1", "example/a"); err != nil {
		t.Fatal(err)
	}
	state, err = ref.StateOf("ext.command/v1", "example/a", a)
	if err != nil || state != trust.Untrusted {
		t.Fatalf("revoked state = %v, %v", state, err)
	}
	state, err = ref.StateOf("inference.provider/v1", "example/a", b)
	if err != nil || state != trust.Trusted {
		t.Fatalf("other scope lost = %v, %v", state, err)
	}
}
