package arch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/core/plugin"
	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
)

// Every adapter in the repository is an ext package: a ext.json template
// that the adapter loader accepts on every platform we ship, and no adapter
// directory still carries the old manifest or two manifests at once.
func TestEveryAdapterSourceLoadsOnEveryPlatform(t *testing.T) {
	root := repoRoot(t)
	var directories []string
	for _, pattern := range []string{"adapters/*/ext.json", "examples/adapters/*/ext.json", "examples/plugins/go/adapters/*/ext.json"} {
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
		data, err := os.ReadFile(filepath.Join(directory, "ext.json"))
		if err != nil {
			t.Fatal(err)
		}
		var source struct {
			Contracts []struct {
				Name string `json:"name"`
			} `json:"contracts"`
		}
		if err := json.Unmarshal(data, &source); err != nil {
			t.Fatal(err)
		}
		isCTXAdapter := false
		for _, contract := range source.Contracts {
			if contract.Name == "ext.adapter" {
				isCTXAdapter = true
			}
		}
		if !isCTXAdapter {
			continue
		} // Other plugin kinds use their own host and protocol.
		for _, goos := range []string{"linux", "darwin", "windows"} {
			if _, err := adapter.LoadSource(directory, goos); err != nil {
				t.Errorf("%s on %s: %v", directory, goos, err)
			}
		}
		for _, old := range []string{"adapter.toml", "plugin.toml", "plugin.json"} {
			if _, err := os.Stat(filepath.Join(directory, old)); err == nil {
				t.Errorf("%s still has %s beside ext.json", directory, old)
			}
		}
	}
	// Source-only executable guests may supply a descriptor for generic packaging.
	entries, err := os.ReadDir(filepath.Join(root, "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "adapters", entry.Name(), "ext.json")); err != nil {
			data, readErr := os.ReadFile(filepath.Join(root, "adapters", entry.Name(), "descriptor.json"))
			if readErr != nil {
				t.Errorf("adapters/%s needs ext.json or descriptor.json", entry.Name())
				continue
			}
			var descriptor plugin.Descriptor
			if err := json.Unmarshal(data, &descriptor); err != nil {
				t.Error(err)
				continue
			}
			// The generic executable sealer fills the revision from the built bytes.
			if descriptor.Identity.Revision == "" {
				descriptor.Identity.Revision = "source"
			}
			if err := descriptor.Validate(); err != nil {
				t.Errorf("adapters/%s descriptor: %v", entry.Name(), err)
			}
		}
	}
}
