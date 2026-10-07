package arch_test

import (
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/plugin/adapter"
)

// The maintained manager-app adapters are repository content, so loading them
// is a repository-wide check rather than a unit test of the adapter library.
func TestBundledManagerAppAdaptersLoadOnUnixAndWindows(t *testing.T) {
	for _, name := range []string{"rancher_desktop", "orbstack", "docker_desktop"} {
		directory := filepath.Join("..", "..", "..", "..", "adapters", name)
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
