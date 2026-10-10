package chromium

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exampleUpdateURL = "https://updates.example.test/primary"

func TestLinuxPolicyForceAndBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed", "extensions.json")
	for _, action := range []string{"force", "block"} {
		if _, err := manageLinuxPolicy(exampleExtensionID, exampleUpdateURL, action, path); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var policies map[string][]string
	if err := json.Unmarshal(content, &policies); err != nil {
		t.Fatal(err)
	}
	if got := policies["ExtensionInstallForcelist"]; len(got) != 1 || got[0] != exampleExtensionID+";"+exampleUpdateURL {
		t.Fatalf("wrong force policy: %+v", policies)
	}
	if got := policies["ExtensionInstallBlocklist"]; len(got) != 1 || got[0] != exampleExtensionID {
		t.Fatalf("wrong block policy: %+v", policies)
	}
	for _, action := range []string{"unforce", "unblock"} {
		if _, err := manageLinuxPolicy(exampleExtensionID, exampleUpdateURL, action, path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty policy file remains: %v", err)
	}
}

func TestLinuxPolicyRefusesCompetingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "other.json"), []byte(`{"ExtensionInstallForcelist":["other"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manageLinuxPolicy(exampleExtensionID, exampleUpdateURL, "force", filepath.Join(dir, "extensions.json")); err == nil {
		t.Fatal("added competing policy")
	}
}

func TestWindowsPolicyScriptKeepsExistingValues(t *testing.T) {
	path, script := windowsPolicyScript(`Example\Browser`, exampleExtensionID, exampleUpdateURL, "force")
	if !strings.Contains(path, `Example\Browser\ExtensionInstallForcelist`) || !strings.Contains(script, "while ($entries.Name -contains") || !strings.Contains(script, "New-ItemProperty") {
		t.Fatalf("bad policy script: %s %s", path, script)
	}
	_, script = windowsPolicyScript(`Example\Browser`, exampleExtensionID, exampleUpdateURL, "unblock")
	if !strings.Contains(script, "Remove-ItemProperty") || strings.Contains(script, "Remove-Item -LiteralPath $path") {
		t.Fatalf("policy removal would delete more than its entry: %s", script)
	}
}
