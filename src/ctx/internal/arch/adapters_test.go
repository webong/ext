package arch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
)

// The product adapters live in their own repositories (xallet/tab, zip, vat and
// tip). The checks below hold ext's loader and contracts against their real
// manifests; scripts/adapters.sh fetches them into one directory.
func adaptersDirectory(t *testing.T) string {
	t.Helper()
	directory := os.Getenv("EXT_ADAPTERS")
	if directory == "" {
		t.Skip("set EXT_ADAPTERS=$(scripts/adapters.sh) to check the adapter repositories' manifests")
	}
	return directory
}

// adapterManifests returns the ext.json of every fetched adapter.
func adapterManifests(t *testing.T) []string {
	t.Helper()
	manifests, err := filepath.Glob(filepath.Join(adaptersDirectory(t), "*", "ext.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) == 0 {
		t.Fatalf("no adapter manifests under %s", adaptersDirectory(t))
	}
	return manifests
}

func TestBundledManagerAppAdaptersLoadOnUnixAndWindows(t *testing.T) {
	adapters := adaptersDirectory(t)
	for _, name := range []string{"rancher_desktop", "orbstack", "docker_desktop"} {
		directory := filepath.Join(adapters, name)
		for _, goos := range []string{"darwin", "linux", "windows"} {
			loaded, err := adapter.LoadDirectoryForOS(directory, goos)
			if err != nil {
				t.Errorf("%s on %s: %v", name, goos, err)
				continue
			}
			if !loaded.Manifest.SelfContained || !loaded.IsRuntime("manager") || len(loaded.Manifest.Commands) != 0 {
				t.Errorf("%s on %s has unexpected manifest: %#v", name, goos, loaded.Manifest)
			}
		}
	}
}
