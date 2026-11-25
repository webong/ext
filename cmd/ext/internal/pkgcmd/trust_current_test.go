package pkgcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustIgnoresOldCommandGrants(t *testing.T) {
	home := t.TempDir()
	old := []byte(`{"packages":{"example/tools":"` + strings.Repeat("a", 64) + `"}}`)
	path := filepath.Join(home, "command-trust.json")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	trust, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(trust.Packages) != 0 {
		t.Fatal("old grants were imported")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(old) {
		t.Fatalf("old data changed: %s, %v", current, err)
	}
	if _, err := os.Stat(filepath.Join(home, "packages")); !os.IsNotExist(err) {
		t.Fatalf("discovery created package state: %v", err)
	}
}
