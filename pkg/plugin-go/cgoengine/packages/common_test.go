package packages

import (
	"encoding/hex"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/package"
	"testing"
)

// These tests run in both builds, with and without the C engine.

func TestManifestValidationAgreesWithPackagekit(t *testing.T) {
	m := packagekit.Manifest{APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/a", Revision: "r1"},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.read", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}},
		Artifacts: []packagekit.Artifact{{Name: "main", Path: "main.txt", SHA256: hex.EncodeToString(make([]byte, 32))}}}
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
	m.License = "not valid;"
	if !errors.Is(ValidateManifest(m), plugin.ErrInvalid) {
		t.Fatal("bad license accepted")
	}
}
