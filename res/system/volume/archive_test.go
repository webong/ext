package volume

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExportRestoreAndDigest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "value.txt"), []byte("ext"), 0o600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Export(context.Background(), &archive, root); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	if err := os.WriteFile(archivePath, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	size, digest, err := FileDigest(archivePath)
	if err != nil || size == 0 || len(digest) != 64 {
		t.Fatalf("digest = %d, %q, %v", size, digest, err)
	}
	destination := filepath.Join(t.TempDir(), "destination")
	if err := Restore(context.Background(), archivePath, destination); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(filepath.Join(destination, "nested", "value.txt"))
	if err != nil || string(value) != "ext" {
		t.Fatalf("restored value = %q, err = %v", value, err)
	}
}

func TestRestoreRejectsTraversalAndNonEmptyDestination(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "unsafe.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), archivePath, filepath.Join(t.TempDir(), "destination")); err == nil {
		t.Fatal("traversal archive was accepted")
	}
	destination := t.TempDir()
	if err := os.WriteFile(filepath.Join(destination, "existing"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), archivePath, destination); err == nil {
		t.Fatal("non-empty destination was accepted")
	}
}

func TestRestoreEnforcesDecompressedLimitsWithoutArtifacts(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "large"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	output, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = Export(context.Background(), output, source); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err = RestoreWithLimits(context.Background(), archive, destination, Limits{Bytes: 1024, Entries: 10}); err == nil {
		t.Fatal("unbounded decompressed content")
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed restore left artifacts: %v %v", entries, err)
	}
}

func TestExportRejectsNilDependencies(t *testing.T) {
	if err := ExportRoot(context.Background(), nil, nil); err == nil {
		t.Fatal("nil export dependencies were accepted")
	}
}

func TestArchiveDestinationUsesPortableSeparators(t *testing.T) {
	got, err := archiveDestination("nested/value.txt")
	if err != nil || got != filepath.Join("nested", "value.txt") {
		t.Fatalf("portable nested path = %q, %v", got, err)
	}
	if _, err := archiveDestination(`nested\value.txt`); err == nil {
		t.Fatal("platform-specific archive separator was accepted")
	}
}
