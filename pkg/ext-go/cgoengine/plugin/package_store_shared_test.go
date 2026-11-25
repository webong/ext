//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackageStoreRequiresNativeLibrary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	t.Setenv("EXT_PLUGIN_LIBRARY", filepath.Join(t.TempDir(), "missing-library"))
	if s, err := OpenPackageStore(root, PackageStoreLimits{}); err == nil {
		s.Close()
		t.Fatal("missing native library silently selected a Go store")
	}
	if s, err := OpenLayeredPackageStore([]string{root}, PackageStoreLimits{}); err == nil {
		s.Close()
		t.Fatal("missing native library silently selected a Go view")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("failed open created a store: %v", err)
	}
}
