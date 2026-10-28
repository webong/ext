package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if _, err := store.Install(context.Background(), manifest, files); err != nil {
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

func TestModelCommandRun(t *testing.T) {
	root := modelStoreTestHelper(t)
	// Create an input file.
	inputFile := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(inputFile, []byte{1, 2, 3, 4}, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", root, "onnxruntime/example-model@v1.2.3", inputFile}, &stdout, &stderr); code != 0 {
		t.Fatalf("model run = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "onnxruntime/example-model@v1.2.3") {
		t.Fatalf("run output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "4 bytes") {
		t.Fatalf("run output missing byte count = %q", stdout.String())
	}
}

func TestModelCommandRunMissingInput(t *testing.T) {
	root := modelStoreTestHelper(t)
	var stdout, stderr bytes.Buffer
	if code := modelCommand([]string{"run", root, "onnxruntime/example-model@v1.2.3", "/nonexistent.bin"}, &stdout, &stderr); code == 0 {
		t.Fatal("model run with missing input should fail")
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
