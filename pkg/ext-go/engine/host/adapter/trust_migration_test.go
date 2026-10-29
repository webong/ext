package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
)

func TestLegacyAdapterTrustMigratesAndRevokeStaysRevoked(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	id := "example"
	digest := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(home, "trust"), []byte(id+" "+digest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := store.readTrust()
	if err != nil || records[id] != digest {
		t.Fatalf("migration: %v, %v", records, err)
	}
	file, err := hosttrust.New(store.TrustFile)
	if err != nil {
		t.Fatal(err)
	}
	state, err := file.StateOf(adapterTrustScope, id, digest)
	if err != nil || state != hosttrust.Trusted {
		t.Fatalf("shared trust state: %v, %v", state, err)
	}
	if err := store.RemoveTrust(id); err != nil {
		t.Fatal(err)
	}
	records, err = NewStore(home).readTrust()
	if err != nil || len(records) != 0 {
		t.Fatalf("legacy grant was restored: %v, %v", records, err)
	}
}
