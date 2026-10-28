package ml

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreInstallResolveListRemove(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := validManifest()
	files := map[string][]byte{
		"model.onnx": []byte("model bytes"),
	}
	installed, err := store.InstallFromBytes(context.Background(), manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Directory != filepath.Join(root, "onnxruntime", "example-model", "v1.2.3") {
		t.Fatalf("directory = %q", installed.Directory)
	}
	if installed.Manifest.Name != "example-model" {
		t.Fatalf("name = %q", installed.Manifest.Name)
	}
	// Resolve re-verifies digests.
	resolved, err := store.Resolve(context.Background(), "onnxruntime", "example-model", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Directory != installed.Directory {
		t.Fatalf("resolved directory = %q", resolved.Directory)
	}
	// List returns the model.
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Manifest.Name != "example-model" {
		t.Fatalf("list = %+v", list)
	}
	// Remove deletes it.
	if err := store.Remove(context.Background(), "onnxruntime", "example-model", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), "onnxruntime", "example-model", "v1.2.3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after remove = %v", err)
	}
	list, err = store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list after remove = %+v", list)
	}
}

func TestStoreInstallRejectsDigestMismatch(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := validManifest()
	files := map[string][]byte{
		"model.onnx": []byte("tampered bytes"),
	}
	if _, err := store.InstallFromBytes(context.Background(), manifest, files); !errors.Is(err, ErrDigest) {
		t.Fatalf("digest mismatch = %v", err)
	}
}

func TestStoreInstallRejectsMissingArtifact(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := validManifest()
	if _, err := store.InstallFromBytes(context.Background(), manifest, map[string][]byte{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing artifact = %v", err)
	}
}

func TestStoreInstallReportsIdenticalAndRejectsDifferent(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := validManifest()
	files := map[string][]byte{"model.onnx": []byte("model bytes")}
	first, err := store.InstallFromBytes(context.Background(), manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	// Identical reinstall is reported as already installed.
	second, err := store.InstallFromBytes(context.Background(), manifest, files)
	if err != nil || second.Directory != first.Directory {
		t.Fatalf("identical reinstall = %+v, %v", second, err)
	}
	// Different manifest at the same reference fails with ErrExist.
	different := validManifest()
	different.Capabilities = []string{"text.generate"}
	differentFiles := map[string][]byte{"model.onnx": []byte("different bytes"), "labels.txt": []byte("labels")}
	different.Artifacts = []Artifact{
		{Role: "model", File: "model.onnx", SHA256: sha256Of("different bytes")},
		{Role: "labels", File: "labels.txt", SHA256: sha256Of("labels")},
	}
	if _, err := store.InstallFromBytes(context.Background(), different, differentFiles); !errors.Is(err, ErrExist) {
		t.Fatalf("different manifest = %v", err)
	}
}

func TestStoreTamperDetection(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := validManifest()
	files := map[string][]byte{"model.onnx": []byte("model bytes")}
	installed, err := store.InstallFromBytes(context.Background(), manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the artifact after install.
	artifactPath := filepath.Join(installed.Directory, "model.onnx")
	if err := os.WriteFile(artifactPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), "onnxruntime", "example-model", "v1.2.3"); !errors.Is(err, ErrDigest) {
		t.Fatalf("tampered resolve = %v", err)
	}
}

func TestStoreRejectsRelativeAndSymlinkRoot(t *testing.T) {
	if _, err := OpenStore("relative/path"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative root = %v", err)
	}
	linkRoot := filepath.Join(t.TempDir(), "link")
	realRoot := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(linkRoot); !errors.Is(err, ErrInvalid) {
		t.Fatalf("symlink root = %v", err)
	}
}

func TestStoreRemovesEmptyParentDirectories(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := validManifest()
	if _, err := store.InstallFromBytes(context.Background(), manifest, map[string][]byte{"model.onnx": []byte("model bytes")}); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background(), "onnxruntime", "example-model", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "onnxruntime")); !os.IsNotExist(err) {
		t.Fatal("provider directory not pruned")
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Fatal("root directory was pruned")
	}
}
