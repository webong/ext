package ml

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func validManifest() Manifest {
	return Manifest{
		APIVersion:   ManifestAPIVersion,
		Name:         "example-model",
		Version:      "v1.2.3",
		Provider:     "onnxruntime",
		Capabilities: []string{"image.classify"},
		Artifacts: []Artifact{
			{Role: "model", File: "model.onnx", SHA256: sha256Of("model bytes")},
		},
	}
}

func sha256Of(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestManifestValidate(t *testing.T) {
	manifest := validManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	// Duplicate capability is rejected.
	duplicate := validManifest()
	duplicate.Capabilities = []string{"image.classify", "image.classify"}
	if err := duplicate.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate capability = %v", err)
	}
	// Bad apiVersion is rejected.
	badAPI := validManifest()
	badAPI.APIVersion = "content.model/v2"
	if err := badAPI.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad apiVersion = %v", err)
	}
	// Empty name is rejected.
	emptyName := validManifest()
	emptyName.Name = ""
	if err := emptyName.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name = %v", err)
	}
	// Too many capabilities is rejected.
	manyCaps := validManifest()
	caps := make([]string, MaxCapabilities+1)
	for i := range caps {
		caps[i] = "cap" + string(rune('a'+i%26))
	}
	manyCaps.Capabilities = caps
	if err := manyCaps.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many capabilities = %v", err)
	}
	// Duplicate artifact file is rejected.
	dupFile := validManifest()
	dupFile.Artifacts = append(dupFile.Artifacts, Artifact{Role: "labels", File: "model.onnx", SHA256: sha256Of("other")})
	if err := dupFile.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate artifact file = %v", err)
	}
}

func TestManifestValidateArtifact(t *testing.T) {
	artifact := Artifact{Role: "model", File: "model.onnx", SHA256: sha256Of("data")}
	if err := artifact.Validate(); err != nil {
		t.Fatal(err)
	}
	// Bad digest length is rejected.
	badDigest := artifact
	badDigest.SHA256 = "not64chars"
	if err := badDigest.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad digest = %v", err)
	}
	// Non-hex digest is rejected.
	nonHex := artifact
	nonHexBytes := make([]byte, 64)
	for i := range nonHexBytes {
		nonHexBytes[i] = 'z'
	}
	nonHex.SHA256 = string(nonHexBytes)
	if err := nonHex.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-hex digest = %v", err)
	}
}

func TestReferenceFrom(t *testing.T) {
	ref, err := ReferenceFrom("onnxruntime/example-model@v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Provider != "onnxruntime" || ref.Name != "example-model" || ref.Version != "v1.2.3" {
		t.Fatalf("reference = %+v", ref)
	}
	if ref.String() != "onnxruntime/example-model@v1.2.3" {
		t.Fatalf("string = %q", ref.String())
	}
	if ref.CanonicalKey() != "onnxruntime\x00example-model\x00v1.2.3" {
		t.Fatalf("canonical key = %q", ref.CanonicalKey())
	}
	for _, bad := range []string{"", "onnxruntime", "onnxruntime/example-model", "onnxruntime/example-model@", "@example@v1", "//example@v1", "onnxruntime//example@v1"} {
		if _, err := ReferenceFrom(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q = %v", bad, err)
		}
	}
}

func TestTensorValidate(t *testing.T) {
	tensor, err := FromFloat32([]int{1, 3, 2, 2}, []float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	if err != nil {
		t.Fatal(err)
	}
	if tensor.Elements() != 12 {
		t.Fatalf("elements = %d", tensor.Elements())
	}
	if len(tensor.Data) != 48 {
		t.Fatalf("data len = %d", len(tensor.Data))
	}
	values := tensor.Float32()
	if len(values) != 12 || values[0] != 1 || values[11] != 12 {
		t.Fatalf("float32 = %v", values)
	}
	// Bad format is rejected.
	bad := Tensor{Format: "float64"}
	if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad format = %v", err)
	}
	// Wrong data length is rejected.
	short := Tensor{Shape: []int{2}, Format: "float32", Data: []byte{1, 2, 3}}
	if err := short.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short data = %v", err)
	}
	// Too many dimensions is rejected.
	manyDims := Tensor{Shape: make([]int, 9), Format: "float32", Data: make([]byte, 36)}
	if err := manyDims.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("many dims = %v", err)
	}
}

func TestManifestValidateArtifactRoles(t *testing.T) {
	manifest := validManifest()
	roles, err := validateArtifactRoles(manifest)
	if err != nil || len(roles) != 1 || roles[0] != "model" {
		t.Fatalf("roles = %v, %v", roles, err)
	}
}
