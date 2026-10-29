package pkgcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
)

func TestCommandTrustLegacyImportIsOneTime(t *testing.T) {
	home := t.TempDir()
	id := "example/tools"
	digest := strings.Repeat("b", 64)
	legacy := `{"packages":{"example/tools":"` + digest + `"}}`
	if err := os.WriteFile(filepath.Join(home, TrustFile), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := LoadTrust(home)
	if err != nil || first.Packages[id] != digest {
		t.Fatalf("legacy import: %v, %v", first, err)
	}
	file, err := hosttrust.New(filepath.Join(home, sharedTrustFile))
	if err != nil {
		t.Fatal(err)
	}
	state, err := file.StateOf(scope, id, digest)
	if err != nil || state != hosttrust.Trusted {
		t.Fatalf("shared trust record: %v, %v", state, err)
	}
	if err := first.Revoke(id); err != nil {
		t.Fatal(err)
	}
	again, err := LoadTrust(home)
	if err != nil || len(again.Packages) != 0 {
		t.Fatalf("revoke reimported legacy grant: %v, %v", again, err)
	}
}
