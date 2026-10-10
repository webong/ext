package chromium

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixStoreRequestAndRemoval(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	config := exampleStore()
	directory := filepath.Join(root, ".config", "example", "External Extensions")
	result, err := changeStoreInstall("linux", "example", config, "primary", exampleExtensionID, directory, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "requested" || result.Browser != "example" {
		t.Fatalf("unexpected result: %+v", result)
	}
	content, err := os.ReadFile(result.Source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), config.UpdateURLs["primary"]) {
		t.Fatalf("wrong store: %s", content)
	}
	if _, err := changeStoreInstall("linux", "example", config, "secondary", exampleExtensionID, directory, true); err == nil {
		t.Fatal("removed a request for a different store")
	}
	removed, err := changeStoreInstall("linux", "example", config, "primary", exampleExtensionID, directory, true)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Status != "request-removed" {
		t.Fatalf("unexpected removal: %+v", removed)
	}
	if _, err := os.Stat(result.Source); !os.IsNotExist(err) {
		t.Fatalf("request still present: %v", err)
	}
}

func TestExternalRequestRejectsUnknownStoreAndPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	config := exampleStore()
	if _, err := changeStoreInstall("linux", "example", config, "unknown", exampleExtensionID, "", false); err == nil {
		t.Fatal("accepted a store the adapter does not declare")
	}
	if _, err := changeStoreInstall("linux", "example", config, "primary", "invalid", "", false); err == nil {
		t.Fatal("accepted an invalid extension ID")
	}
	if _, err := changeStoreInstall("linux", "example", config, "primary", exampleExtensionID, filepath.Join(t.TempDir(), "arbitrary"), false); err == nil {
		t.Fatal("accepted an undeclared directory")
	}
	if _, err := changeStoreInstall("windows", "example", config, "primary", exampleExtensionID, `C:\Extensions`, false); err == nil {
		t.Fatal("accepted a directory on Windows")
	}
	if _, err := changeStoreInstall("linux", "example", StoreConfig{}, "", exampleExtensionID, "", false); err == nil {
		t.Fatal("accepted an adapter without stores")
	}
}

func TestExternalRequestRejectsSymlinkedPath(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(actual, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("HOME", link)
	directory := filepath.Join(link, ".config", "example", "External Extensions")
	if _, err := changeStoreInstall("linux", "example", exampleStore(), "primary", exampleExtensionID, directory, false); err == nil {
		t.Fatal("accepted symlinked path")
	}
}

func TestWindowsPowerShellRegistryScript(t *testing.T) {
	config := exampleStore()
	script := windowsExternalScript(config, exampleExtensionID, config.UpdateURLs["primary"], false)
	if !strings.Contains(script, `Example\Browser\Extensions\`+exampleExtensionID) || !strings.Contains(script, "SetValue('update_url'") || !strings.Contains(script, config.UpdateURLs["primary"]) {
		t.Fatalf("wrong Windows request: %s", script)
	}
	encoded, err := base64.StdEncoding.DecodeString(encodePowerShell(script))
	if err != nil || len(encoded)%2 != 0 || encoded[0] != '$' || encoded[1] != 0 {
		t.Fatalf("PowerShell command is not UTF-16LE: %v", err)
	}
	script = windowsExternalScript(config, exampleExtensionID, config.UpdateURLs["primary"], true)
	if !strings.Contains(script, "DeleteSubKey") || !strings.Contains(script, "different update URL") {
		t.Fatalf("removal is not guarded: %s", script)
	}
}
