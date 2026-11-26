package pkgcmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestPackExecutableWithoutProductPlugin(t *testing.T) {
	for _, contract := range []string{"model.tensor", "example.custom"} {
		for _, target := range []string{"linux", "windows"} {
			t.Run(contract+"/"+target, func(t *testing.T) {
				root := t.TempDir()
				binary := filepath.Join(root, "guest")
				content := []byte("compiled guest fixture")
				if err := os.WriteFile(binary, content, 0o700); err != nil {
					t.Fatal(err)
				}
				descriptor := plugin.Descriptor{
					APIVersion: plugin.APIVersion,
					Identity:   plugin.Identity{ID: "example", Version: "1.0.0"},
					Contracts:  []plugin.Contract{{ContractRef: plugin.ContractRef{Name: contract, Version: "v1"}, Operations: []plugin.Operation{{Name: "infer", Surface: "action"}}}},
				}
				data, err := json.Marshal(descriptor)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "descriptor.json")
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				out := filepath.Join(root, "sealed")
				args := []string{"pack", "--descriptor", path, "--protocol", contract + "/v1", "--binary", binary, "--output", out, "--os", target, "--arch", "amd64"}
				var stdout, stderr bytes.Buffer
				// No installed products, package store, or trust configuration is needed.
				if code := (Manager{}).Run(args, &stdout, &stderr); code != 0 {
					t.Fatalf("pack: %d: %s", code, &stderr)
				}
				data, err = os.ReadFile(filepath.Join(out, "ext.json"))
				if err != nil {
					t.Fatal(err)
				}
				var manifest packagekit.Manifest
				if err := json.Unmarshal(data, &manifest); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(content)
				if manifest.Descriptor.Identity.Revision != hex.EncodeToString(digest[:]) || manifest.Artifacts[0].OS != target || manifest.Entrypoints[0].Protocols[0] != contract+"/v1" {
					t.Fatalf("incorrect sealed manifest: %+v", manifest)
				}
				s, err := store.NewWithLayout(filepath.Join(root, "installed"), store.Limits{}, storelayout.Ext())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.Install(out); err != nil {
					t.Fatalf("install sealed package: %v", err)
				}
				if code := (Manager{}).Run(args, &stdout, &stderr); code == 0 {
					t.Fatal("overwrote existing package")
				}
				args[4] = "undeclared/v1"
				args[8] = filepath.Join(root, "invalid")
				if code := (Manager{}).Run(args, &stdout, &stderr); code == 0 {
					t.Fatal("accepted undeclared protocol")
				}
				if _, err := os.Stat(args[8]); !os.IsNotExist(err) {
					t.Fatal("invalid package was published")
				}
			})
		}
	}
}
