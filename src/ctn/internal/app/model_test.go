package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/res/ml"
)

// modelStoreTestHelper creates a temp store with one installed model.
func modelStoreTestHelper(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	store, err := ml.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := ml.Manifest{
		APIVersion:   ml.ManifestAPIVersion,
		Name:         "example-model",
		Version:      "v1.2.3",
		Provider:     "onnxruntime",
		Capabilities: []string{"image.classify"},
		Artifacts: []ml.Artifact{
			{Role: "model", File: "model.onnx", SHA256: sha256Hex("model bytes")},
		},
	}
	files := map[string][]byte{"model.onnx": []byte("model bytes")}
	if _, err := store.InstallFromBytes(context.Background(), manifest, files); err != nil {
		t.Fatal(err)
	}
	return root
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestModelCommandList(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"list", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("model list = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "onnxruntime/example-model@v1.2.3") {
		t.Fatalf("list output = %q", stdout.String())
	}
}

func TestModelCommandListMissingDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"list", "/nonexistent-dir"}, &stdout, &stderr); code == 0 {
		t.Fatal("model list on missing dir should fail")
	}
}

func TestModelCommandInspect(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"inspect", root, "onnxruntime/example-model@v1.2.3"}, &stdout, &stderr); code != 0 {
		t.Fatalf("model inspect = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example-model") {
		t.Fatalf("inspect output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "onnxruntime") {
		t.Fatalf("inspect output missing provider = %q", stdout.String())
	}
}

func TestModelCommandInspectMissingModel(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"inspect", root, "onnxruntime/missing@v9.9.9"}, &stdout, &stderr); code == 0 {
		t.Fatal("model inspect of missing model should fail")
	}
}

func TestModelCommandRunRequiresProvider(t *testing.T) {
	root := modelStoreTestHelper(t)
	inputFile := writeTensorInput(t, []mlTensorInput{{
		Shape:  []int{1, 4},
		Format: "float32",
		Data:   []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}})
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", root, "onnxruntime/example-model@v1.2.3", inputFile}, &stdout, &stderr); code == 0 {
		t.Fatal("model run without --provider should fail")
	}
}

func TestModelCommandRunNoProviderAvailable(t *testing.T) {
	root := modelStoreTestHelper(t)
	inputFile := writeTensorInput(t, []mlTensorInput{{
		Shape:  []int{1, 4},
		Format: "float32",
		Data:   []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}})
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", "--provider", "nonexistent", root, "onnxruntime/example-model@v1.2.3", inputFile}, &stdout, &stderr); code == 0 {
		t.Fatal("model run with missing provider should fail")
	}
	if !strings.Contains(stderr.String(), "no provider registered") {
		t.Fatalf("expected 'no provider registered' error, got: %q", stderr.String())
	}
}

func TestModelCommandRunMissingInput(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", "--provider", "onnxruntime", root, "onnxruntime/example-model@v1.2.3", "/nonexistent.bin"}, &stdout, &stderr); code == 0 {
		t.Fatal("model run with missing input should fail")
	}
}

func TestModelCommandInstall(t *testing.T) {
	root := t.TempDir()
	manifestFile := filepath.Join(t.TempDir(), "manifest.json")
	manifest := ml.Manifest{
		APIVersion:   ml.ManifestAPIVersion,
		Name:         "test-model",
		Version:      "v1.0.0",
		Provider:     "test-provider",
		Capabilities: []string{"test"},
		Artifacts: []ml.Artifact{
			{Role: "model", File: "model.bin", SHA256: sha256Hex("test data")},
		},
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestFile, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	artifactsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactsDir, "model.bin"), []byte("test data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"install", root, manifestFile, artifactsDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("model install = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "test-provider/test-model@v1.0.0") {
		t.Fatalf("install output = %q", stdout.String())
	}
}

func TestModelCommandRemove(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"remove", root, "onnxruntime/example-model@v1.2.3"}, &stdout, &stderr); code != 0 {
		t.Fatalf("model remove = %d: %s", code, stderr.String())
	}
	if _, err := ml.OpenStore(root); err != nil {
		t.Fatal(err)
	}
	// Verify it's gone.
	store, _ := ml.OpenStore(root)
	if _, err := store.Resolve(context.Background(), "onnxruntime", "example-model", "v1.2.3"); err == nil {
		t.Fatal("removed model still found")
	}
}

func TestModelCommandVerify(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"verify", root, "onnxruntime/example-model@v1.2.3"}, &stdout, &stderr); code != 0 {
		t.Fatalf("model verify = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "verified") {
		t.Fatalf("verify output = %q", stdout.String())
	}
}

func TestModelCommandUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{}, &stdout, &stderr); code != 2 {
		t.Fatalf("model with no args = %d", code)
	}
	if code := modelCommand([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("model with bogus action = %d", code)
	}
}

func writeTensorInput(t *testing.T, tensors []mlTensorInput) string {
	t.Helper()
	input := map[string]interface{}{"tensors": tensors}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
