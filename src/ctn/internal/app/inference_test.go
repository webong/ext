package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
	ginference "github.com/webong/ext/pkg/ext-go/inference"
)

func TestInferenceProviderRequiresTrustedPackage(t *testing.T) {
	source := t.TempDir()
	binary := []byte("test provider executable")
	if err := os.WriteFile(filepath.Join(source, "provider"), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	manifest := packagekit.Manifest{
		APIVersion:  packagekit.Version,
		Descriptor:  ginference.Descriptor(ginference.Identity{ID: "review", Revision: "r1", Version: "1"}),
		Artifacts:   []packagekit.Artifact{{Name: "main", Path: "provider", SHA256: hex.EncodeToString(sum[:])}},
		Entrypoints: []packagekit.Entrypoint{{Name: "main", Runtime: "argv", Artifact: "main", Protocols: []string{"inference.provider/v1"}}},
	}
	if err := store.WriteManifest(source, manifest); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "packages")
	t.Setenv("EXT_INFERENCE_PACKAGE_ROOT", root)
	t.Setenv("EXT_HOME", t.TempDir())
	var output, diagnostics bytes.Buffer
	if code := inferenceInstallCommand([]string{source}, &output, &diagnostics); code != 0 {
		t.Fatalf("install exit %d: %s", code, diagnostics.String())
	}
	s, err := store.New(root, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Load("review")
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 {
		t.Fatal("install did not print digest")
	}
	if _, err := resolveProvider("review"); err == nil {
		t.Fatal("untrusted provider was accepted")
	}
	digest, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	output.Reset()
	diagnostics.Reset()
	if code := inferenceTrustCommand([]string{"review"}, &output, &diagnostics); code != 0 {
		t.Fatalf("trust exit %d: %s", code, diagnostics.String())
	}
	if _, err := resolveProvider("review"); err != nil {
		t.Fatalf("scoped trust did not authorize provider: %v", err)
	}
	if code := inferenceUntrustCommand([]string{"review"}, &output, &diagnostics); code != 0 {
		t.Fatalf("untrust exit %d: %s", code, diagnostics.String())
	}
	if _, err := resolveProvider("review"); err == nil {
		t.Fatal("revoked provider was accepted")
	}
	t.Setenv("EXT_INFERENCE_TRUST_REVIEW", digest)
	cfg, err := resolveProvider("review")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path == "" || cfg.Digest != digest {
		t.Fatalf("resolved wrong package: %+v", cfg)
	}
	if err := os.WriteFile(cfg.Path, []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProvider("review"); err == nil {
		t.Fatal("tampered provider was accepted")
	}
}
