package pkgcmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
)

func TestMigrationPreservesScopesAndDoesNotReimport(t *testing.T) {
	home := t.TempDir()
	installed := install(t, filepath.Join(home, "commands"), "1.0.0", declares)
	digest, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	old, _ := hosttrust.New(filepath.Join(home, "package-trust.json"))
	if err := old.Record(scope, installed.Name(), digest); err != nil {
		t.Fatal(err)
	}
	if err := old.Record("ctn.inference/v1", installed.Name(), digest); err != nil {
		t.Fatal(err)
	}
	if err := RequireMigration(home); err == nil {
		t.Fatal("legacy installation not detected")
	}
	if err := migrate(home); err != nil {
		t.Fatal(err)
	}
	if err := RequireMigration(home); err != nil {
		t.Fatal(err)
	}
	fresh, _ := store.New(filepath.Join(home, "packages"), store.Limits{})
	p, err := fresh.Load(installed.Name())
	if err != nil {
		t.Fatal(err)
	}
	trust, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := trust.Trusted(p); err != nil || !ok {
		t.Fatalf("trust lost: %v %v", ok, err)
	}
	if rows, err := trust.file.Records("ctx.adapter/v2"); err != nil || len(rows) != 0 {
		t.Fatalf("scope expanded: %v %v", rows, err)
	}
	if rows, err := trust.file.Records("ctn.inference/v1"); err != nil || len(rows) != 1 {
		t.Fatalf("consumer scope lost: %v %v", rows, err)
	}
	if err := trust.Revoke(p.Name()); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Remove(p.Name()); err != nil {
		t.Fatal(err)
	}
	if err := migrate(home); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Load(p.Name()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("package reimported: %v", err)
	}
	trust, err = LoadTrust(home)
	if err != nil || len(trust.Packages) != 0 {
		t.Fatalf("trust reimported: %v %v", trust, err)
	}
	if _, err := os.Stat(installed.Directory); err != nil {
		t.Fatal("legacy backup changed", err)
	}
}

func TestMigrationCollisionAndTamperingLeaveNoPublishedStore(t *testing.T) {
	for _, kind := range []string{"collision", "tampering", "trust-collision"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			first := install(t, filepath.Join(home, "commands"), "1.0.0", declares)
			switch kind {
			case "collision":
				install(t, filepath.Join(home, "adapters"), "2.0.0", declares)
			case "tampering":
				path, err := first.ArtifactPath(first.Manifest.Artifacts[0].Name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("damaged"), 0700); err != nil {
					t.Fatal(err)
				}
			case "trust-collision":
				if err := os.MkdirAll(filepath.Join(home, "adapters"), 0700); err != nil {
					t.Fatal(err)
				}
				for i, path := range []string{filepath.Join(home, "package-trust.json"), filepath.Join(home, "adapters", "package-trust.json")} {
					ledger, _ := hosttrust.New(path)
					if err := ledger.Record("ctx.adapter/v2", first.Name(), strings.Repeat(string(rune('a'+i)), 64)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := migrate(home); err == nil {
				t.Fatal("invalid migration succeeded")
			}
			if _, err := os.Stat(filepath.Join(home, "packages")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("published incomplete store: %v", err)
			}
			if _, err := os.Stat(first.Directory); err != nil {
				t.Fatal("legacy source lost", err)
			}
		})
	}
}

func TestMigrationLegacyAdapterTrustAndReceipt(t *testing.T) {
	home := t.TempDir()
	p := install(t, filepath.Join(home, "adapters"), "1.0.0", "")
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "adapters", "trust"), []byte(p.Name()+" "+digest+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	receipts := filepath.Join(home, "adapters", ".receipts")
	if err := os.MkdirAll(receipts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receipts, "receipt.json"), []byte("receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	// An abandoned staging directory is never consulted as installed state.
	if err := os.Mkdir(filepath.Join(home, ".packages-abandoned"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := migrate(home); err != nil {
		t.Fatal(err)
	}
	trust, err := LoadTrust(home)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := trust.file.Records("ctx.adapter/v2"); err != nil || len(rows) != 1 || rows[0].Digest != digest {
		t.Fatalf("adapter trust: %v %v", rows, err)
	}
	if rows, err := trust.file.Records(scope); err != nil || len(rows) != 0 {
		t.Fatalf("adapter trust became command trust: %v %v", rows, err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "packages", ".receipts", "receipt.json")); err != nil || string(data) != "receipt" {
		t.Fatalf("receipt: %q %v", data, err)
	}
}
