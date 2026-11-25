package sourcecmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestCatalogImportCollisionIsAtomic(t *testing.T) {
	lib := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if lib == "" {
		t.Skip("run scripts/package-acceptance.sh for C catalog import acceptance")
	}
	t.Setenv("EXT_PLUGIN_LIBRARY", lib)
	catalog, err := store.New(t.TempDir(), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"example/a", "example/z"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "data"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		manifest, err := packagekit.SourceManifest(id, "1.0.0", []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.data", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}, dir, []string{"data"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.WriteManifest(dir, manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.Install(dir); err != nil {
			t.Fatal(err)
		}
	}
	m := Manager{Home: t.TempDir()}
	index := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(index, []byte(`{"format_version":1,"name":"example/z","packages":{"linux/amd64":{"url":"z.zip","sha256":"`+strings.Repeat("a", 64)+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("example/z"))
	name := fmt.Sprintf("catalog-%x", h[:8])
	if err := m.run("add", []string{name, index}, io.Discard); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(m.Home, "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.importCatalog(catalog.Root(), io.Discard); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("collision: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(m.Home, "sources.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("collision published configuration: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(m.Home, "source-archives"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("abandoned import: %v %v", entries, err)
	}
}

func TestArchiveCatalogRejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "large"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(store.DefaultMaxArchiveBytes + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archiveDirectory(dir, filepath.Join(t.TempDir(), "archive.zip")); err == nil {
		t.Fatal("accepted oversized catalog file")
	}
}
