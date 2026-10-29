package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogImportVerifyListAndRemove(t *testing.T) {
	root := filepath.Join(t.TempDir(), "catalog")
	catalog, err := InitCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	source := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	item, err := catalog.Import(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	if item.Name != "note.txt" || item.Size != 5 || item.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("record: %+v", item)
	}
	if err := os.WriteFile(source, []byte("source changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspected, err := catalog.Inspect(context.Background(), item.ID)
	if err != nil || inspected != item {
		t.Fatalf("inspect: %+v, %v", inspected, err)
	}
	listed, err := catalog.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0] != item {
		t.Fatalf("list: %+v, %v", listed, err)
	}
	if err := os.WriteFile(filepath.Join(root, "items", item.ID, "content"), []byte("tamper"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Inspect(context.Background(), item.ID); !errors.Is(err, ErrCatalogInvalid) {
		t.Fatalf("tamper accepted: %v", err)
	}
	if err := catalog.Remove(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Inspect(context.Background(), item.ID); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatalf("removed item: %v", err)
	}
}

func TestCatalogRejectsLinksAndTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "catalog")
	catalog, err := InitCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(source, link); err == nil {
		if _, err := catalog.Import(context.Background(), link); !errors.Is(err, ErrCatalogInvalid) {
			t.Fatalf("source link accepted: %v", err)
		}
	}
	if _, err := catalog.Inspect(context.Background(), "../outside"); !errors.Is(err, ErrCatalogInvalid) {
		t.Fatalf("traversal accepted: %v", err)
	}
	if err := catalog.Remove("../outside"); !errors.Is(err, ErrCatalogInvalid) {
		t.Fatalf("remove traversal accepted: %v", err)
	}
}
