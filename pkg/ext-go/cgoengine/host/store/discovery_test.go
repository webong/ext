package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

func TestResolveEntrypointRequiresTrustAndPrefersPlatform(t *testing.T) {
	source := t.TempDir()
	artifacts := make([]packagekit.Artifact, 0, 2)
	for _, name := range []string{"generic", "native"} {
		data := []byte(name)
		if err := os.WriteFile(filepath.Join(source, name), data, 0o700); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		artifacts = append(artifacts, packagekit.Artifact{Name: name, Path: name, SHA256: hex.EncodeToString(sum[:])})
	}
	manifest := packagekit.Manifest{
		APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "discovery", Revision: "r1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example", Version: "v1"}, Operations: []plugin.Operation{{Name: "run"}}}}},
		Artifacts:  artifacts,
		Entrypoints: []packagekit.Entrypoint{
			{Name: "main", Runtime: "argv", Artifact: "generic", Protocols: []string{"example/v1"}},
			{Name: "main." + runtime.GOOS, Runtime: "argv", Artifact: "native", Protocols: []string{"example/v1"}},
		},
	}
	if err := store.WriteManifest(source, manifest); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(t.TempDir(), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveEntrypoint("discovery", "example", "v1", "example/v1", ""); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("missing trust: %v", err)
	}
	if _, err := s.ResolveEntrypoint("discovery", "example", "v1", "example/v1", "wrong"); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("wrong trust: %v", err)
	}
	selected, err := s.ResolveEntrypoint("discovery", "example", "v1", "example/v1", digest)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Artifact != "native" {
		t.Fatalf("selected %s", selected.Artifact)
	}
	if _, err := s.ResolveEntrypoint("discovery", "different", "v1", "example/v1", digest); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("wrong contract: %v", err)
	}
}
