package arch_test

import (
	"os"
	"path/filepath"
	"testing"

	adapter "github.com/webong/ext/pkg/ext-go/engine/host/adapter"
)

// Every process adapter in the repository is an ext package: a plugin.json template
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
	// Embedded platform libraries have native SDK entry points rather than a process
	// manifest. Keep this explicit so a missing manifest never silently passes.
	embedded := map[string]string{
		"webkit":          "ios/Sources/ExtWebKit/BrowserWorkspace.swift",
		"android_webview": "android/src/main/java/io/github/webong/ext/web/android/BrowserWorkspace.java",
	}
	for name, source := range embedded {
		if _, err := os.Stat(filepath.Join(root, "adapters", name, source)); err != nil {
			t.Errorf("embedded adapter %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, "adapters", name, "plugin.json")); !os.IsNotExist(err) {
			t.Errorf("embedded adapter %s must not declare a process plugin", name)
		}
	}
	// Every other adapter directory must have a manifest.
	entries, err := os.ReadDir(filepath.Join(root, "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || embedded[entry.Name()] != "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "adapters", entry.Name(), "plugin.json")); err != nil {
			t.Errorf("adapters/%s has no plugin.json", entry.Name())
		}
	}
}

// The SDK must ship the owning adapter's sources, not an independently edited copy.
func TestWebKitSDKSourceOwnership(t *testing.T) {
	root := repoRoot(t)
	got, err := filepath.EvalSymlinks(filepath.Join(root, "pkg", "ext-ios", "Sources", "ExtWebKit"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "adapters", "webkit", "ios", "Sources", "ExtWebKit")
	if got != want {
		t.Fatalf("ExtWebKit source = %s, want %s", got, want)
	}
}
