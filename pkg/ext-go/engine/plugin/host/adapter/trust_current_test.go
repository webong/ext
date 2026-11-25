package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustIgnoresOldAdapterGrants(t *testing.T) {
	home := t.TempDir()
	data := []byte("example " + strings.Repeat("a", 64) + "\n")
	path := filepath.Join(home, "trust")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(home)
	records, err := s.readTrust()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatal("old adapter grants were imported")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("old data changed: %s, %v", after, err)
	}
}
