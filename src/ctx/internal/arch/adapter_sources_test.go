package arch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
)

// Every example adapter in the repository is an ext package: a ext.json
// template that the adapter loader accepts on every platform we ship, and no
// adapter directory still carries the old manifest or two manifests at once.
// Product adapters live in their own repositories (xallet/tab, zip, vat and
// tip), which check their own manifests.
func TestEveryAdapterSourceLoadsOnEveryPlatform(t *testing.T) {
	root := repoRoot(t)
	var directories []string
	for _, pattern := range []string{"examples/adapters/*/ext.json", "examples/plugins/go/adapters/*/ext.json"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			directories = append(directories, filepath.Dir(match))
		}
	}
	if len(directories) < 2 {
		t.Fatalf("found %d example adapter manifests, expected at least 2", len(directories))
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
}
