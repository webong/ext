package graphapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentStoreDoesNotReadOldSQLite(t *testing.T) {
	if os.Getenv("EXT_LEDGER_LIBRARY") == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	home := t.TempDir()
	path := filepath.Join(home, "graph.db")
	data := []byte("old database must not be read or rewritten")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	g, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("old data changed: %s, %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(home, Directory)); err != nil {
		t.Fatal(err)
	}
}
