package ml

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// contractFixture holds the language-neutral contract test data.
type contractFixture struct {
	ManifestAPIVersion   string `json:"manifestAPIVersion"`
	MaxNameLength        int    `json:"maxNameLength"`
	MaxCapabilities      int    `json:"maxCapabilities"`
	MaxArtifacts         int    `json:"maxArtifacts"`
	MaxManifestBytes     int    `json:"maxManifestBytes"`
	ValidManifest        Manifest                      `json:"validManifest"`
	InvalidApiVersion    Manifest                      `json:"invalidApiVersionManifest"`
	EmptyName            Manifest                      `json:"emptyNameManifest"`
	DuplicateCapability  Manifest                      `json:"duplicateCapabilityManifest"`
	DuplicateArtifactFile Manifest                     `json:"duplicateArtifactFileManifest"`
	InvalidDigestArtifact Artifact                     `json:"invalidDigestArtifact"`
	NonHexDigestArtifact  Artifact                     `json:"nonHexDigestArtifact"`
	ValidTensor          Tensor                        `json:"validTensor"`
	InvalidFormatTensor   Tensor                       `json:"invalidFormatTensor"`
	WrongDataLengthTensor Tensor                     `json:"wrongDataLengthTensor"`
	TooManyDimensionsTensor Tensor                   `json:"tooManyDimensionsTensor"`
	ZeroDimensionTensor   Tensor                      `json:"zeroDimensionTensor"`
	ReferenceValid       string   `json:"referenceValid"`
	ReferenceCanonicalKey string  `json:"referenceCanonicalKey"`
	ReferenceInvalid     []string `json:"referenceInvalid"`
	DigestOfModelBytes   string   `json:"digestOfModelBytes"`
	DigestOfTamperedBytes string   `json:"digestOfTamperedBytes"`
}

func loadContractFixture(t *testing.T) contractFixture {
	t.Helper()
	path := filepath.Join("testdata", "contract.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract fixture: %v", err)
	}
	var f contractFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse contract fixture: %v", err)
	}
	return f
}

func TestContractFixtureValidManifest(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.ValidManifest.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestContractFixtureInvalidApiVersion(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.InvalidApiVersion.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid apiVersion accepted: %v", err)
	}
}

func TestContractFixtureEmptyName(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.EmptyName.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name accepted: %v", err)
	}
}

func TestContractFixtureDuplicateCapability(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.DuplicateCapability.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate capability accepted: %v", err)
	}
}

func TestContractFixtureDuplicateArtifactFile(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.DuplicateArtifactFile.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate artifact file accepted: %v", err)
	}
}

func TestContractFixtureInvalidDigestArtifact(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.InvalidDigestArtifact.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid digest accepted: %v", err)
	}
}

func TestContractFixtureNonHexDigestArtifact(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.NonHexDigestArtifact.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-hex digest accepted: %v", err)
	}
}

func TestContractFixtureValidTensor(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.ValidTensor.Validate(); err != nil {
		t.Fatalf("valid tensor rejected: %v", err)
	}
	if f.ValidTensor.Elements() != f.ValidTensorElementCount() {
		t.Fatalf("tensor element count mismatch")
	}
}

func (t contractFixture) ValidTensorElementCount() int {
	count := 1
	for _, dim := range t.ValidTensor.Shape {
		count *= dim
	}
	return count
}

func TestContractFixtureInvalidFormatTensor(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.InvalidFormatTensor.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid format tensor accepted: %v", err)
	}
}

func TestContractFixtureWrongDataLengthTensor(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.WrongDataLengthTensor.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong data length tensor accepted: %v", err)
	}
}

func TestContractFixtureTooManyDimensionsTensor(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.TooManyDimensionsTensor.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many dimensions tensor accepted: %v", err)
	}
}

func TestContractFixtureZeroDimensionTensor(t *testing.T) {
	f := loadContractFixture(t)
	if err := f.ZeroDimensionTensor.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero dimension tensor accepted: %v", err)
	}
}

func TestContractFixtureReference(t *testing.T) {
	f := loadContractFixture(t)
	ref, err := ReferenceFrom(f.ReferenceValid)
	if err != nil {
		t.Fatal(err)
	}
	if ref.String() != f.ReferenceValid {
		t.Fatalf("reference string = %q, want %q", ref.String(), f.ReferenceValid)
	}
	if ref.CanonicalKey() != f.ReferenceCanonicalKey {
		t.Fatalf("canonical key = %q, want %q", ref.CanonicalKey(), f.ReferenceCanonicalKey)
	}
	for _, bad := range f.ReferenceInvalid {
		if _, err := ReferenceFrom(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid reference %q accepted", bad)
		}
	}
}

func TestContractFixtureDigestValues(t *testing.T) {
	f := loadContractFixture(t)
	if f.DigestOfModelBytes != sha256Of("model bytes") {
		t.Fatalf("digest of model bytes mismatch")
	}
	if f.DigestOfTamperedBytes != sha256Of("tampered bytes") {
		t.Fatalf("digest of tampered bytes mismatch")
	}
}
