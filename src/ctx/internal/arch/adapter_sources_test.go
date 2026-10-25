package arch_test

import (
	"os"
	"path/filepath"
	"testing"

	adapter "github.com/webong/ext/pkg/ext-go/engine/host/adapter"
)

// Every adapter in the repository is an ext package: a plugin.json template
// that the adapter loader accepts on every platform we ship, and no adapter
// directory still carries the old manifest or two manifests at once.
func TestEveryAdapterSourceLoadsOnEveryPlatform(t *testing.T) {
	root := repoRoot(t)
	var directories []string
	for _, pattern := range []string{"adapters/*/plugin.json", "examples/adapters/*/plugin.json", "pkg/ext-go/examples/adapters/*/plugin.json"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			directories = append(directories, filepath.Dir(match))
		}
	}
	if len(directories) < 40 {
		t.Fatalf("found %d adapter manifests, expected at least 40", len(directories))
	}
	for _, directory := range directories {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			if _, err := adapter.LoadSource(directory, goos); err != nil {
				t.Errorf("%s on %s: %v", directory, goos, err)
			}
		}
		for _, old := range []string{"adapter.toml", "plugin.toml"} {
			if _, err := os.Stat(filepath.Join(directory, old)); err == nil {
				t.Errorf("%s still has %s beside plugin.json", directory, old)
			}
		}
	}
	// no adapter directory lacks the manifest
	entries, err := os.ReadDir(filepath.Join(root, "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "adapters", entry.Name(), "plugin.json")); err != nil {
			t.Errorf("adapters/%s has no plugin.json", entry.Name())
		}
	}
}
