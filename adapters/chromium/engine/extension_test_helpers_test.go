package chromium

import (
	"os"
	"path/filepath"
	"testing"
)

const exampleExtensionID = "abcdefghijklmnopabcdefghijklmnop"

// exampleStore is a neutral product configuration for the shared mechanisms.
func exampleStore() StoreConfig {
	return StoreConfig{
		DefaultStore:         "primary",
		UpdateURLs:           map[string]string{"primary": "https://updates.example.test/primary", "secondary": "https://updates.example.test/secondary"},
		WindowsVendor:        `Example\Browser`,
		MacUserDirectory:     "Library/Application Support/Example/External Extensions",
		LinuxDirectory:       ".config/example/External Extensions",
		LinuxDirectoryInHome: true,
	}
}

func extensionFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"manifest_version":3,"name":"Example","version":"1.0","permissions":["storage"],"host_permissions":["https://example.com/*"]}`
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "background.js"), []byte("console.log('ready')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}
