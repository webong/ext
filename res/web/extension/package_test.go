package extension

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"manifest_version":3,"name":"Example","version":"1.0","permissions":["storage"],"host_permissions":["https://example.com/*"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "background.js"), []byte("console.log('ready')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPackageAndStageRoundTrip(t *testing.T) {
	source := fixture(t)
	first, err := Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "Example" || first.ID != "" || first.Files != 2 || first.Revision == "" {
		t.Fatalf("unexpected description: %+v", first)
	}
	archive := filepath.Join(t.TempDir(), "example.zip")
	packed, err := Package(source, archive)
	if err != nil {
		t.Fatal(err)
	}
	if packed.Revision != first.Revision {
		t.Fatal("package revision changed")
	}
	fromZIP, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if fromZIP.Revision != first.Revision {
		t.Fatal("ZIP revision changed")
	}
	destination := filepath.Join(t.TempDir(), "installed-source")
	staged, err := Stage(archive, destination)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Revision != first.Revision {
		t.Fatal("staged revision changed")
	}
	if _, err := os.Stat(filepath.Join(destination, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(archive, destination); err == nil {
		t.Fatal("overwrote existing staged extension")
	}
}

func TestRejectsUnsafeZIP(t *testing.T) {
	for _, name := range []string{"../escape.js", "/absolute.js", "inner/../../escape.js", "a\\b.js"} {
		t.Run(name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "bad.zip")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			for _, entry := range []string{"manifest.json", name} {
				out, err := writer.Create(entry)
				if err != nil {
					t.Fatal(err)
				}
				if entry == "manifest.json" {
					_, err = out.Write([]byte(`{"manifest_version":3,"name":"Example","version":"1"}`))
				} else {
					_, err = out.Write([]byte("x"))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			writer.Close()
			file.Close()
			if _, err := Inspect(archive); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}

func TestRejectsSymlinkAndOutputInsideSource(t *testing.T) {
	source := fixture(t)
	if _, err := Package(source, filepath.Join(source, "bundle.zip")); err == nil {
		t.Fatal("accepted output inside source")
	}
	if err := os.Symlink(filepath.Join(source, "background.js"), filepath.Join(source, "alias.js")); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(source); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}
