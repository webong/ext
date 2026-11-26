package pkgcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
)

func TestGenericAuthorCommandsNeverInvokeGuests(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	os.Mkdir(source, 0o700)
	manifest := `{"apiVersion":"ext.io/v1","kind":"package","identity":{"id":"custom-provider","version":"1.0.0"},"contracts":[{"name":"example.custom","version":"v1","operations":[{"name":"call","surface":"action"}]}],"artifacts":[{"name":"main","path":"guest","sha256":""}],"entrypoints":[{"name":"main","runtime":"argv","artifact":"main","protocols":["example.custom/v1"]}]}`
	os.WriteFile(filepath.Join(source, "ext.json"), []byte(manifest), 0o600)
	// Cannot be executed: these commands must inspect rather than launch it.
	os.WriteFile(filepath.Join(source, "guest"), []byte("not an executable"), 0o600)
	var out, stderr bytes.Buffer
	manager := Manager{}
	archive := filepath.Join(root, "package.zip")
	for _, args := range [][]string{{"test", source}, {"pack", source, "--output", archive}} {
		if code := manager.Run(args, &out, &stderr); code != 0 {
			t.Fatalf("%v: %d: %s", args, code, &stderr)
		}
	}
	s, err := store.NewWithLayout(filepath.Join(root, "packages"), store.Limits{}, storelayout.Ext())
	if err != nil {
		t.Fatal(err)
	}
	trust, err := LoadTrust(root)
	if err != nil {
		t.Fatal(err)
	}
	manager = Manager{Store: s, Trust: trust}
	// Use the executable sealer to prepare the installed fixture without executing it.
	stage := filepath.Join(root, "sealed")
	descPath := filepath.Join(root, "descriptor.json")
	os.WriteFile(descPath, []byte(`{"apiVersion":"ext.io.pc/v1","identity":{"id":"custom-provider","version":"1.0.0"},"contracts":[{"name":"example.custom","version":"v1","operations":[{"name":"call","surface":"action"}]}]}`), 0o600)
	if code := manager.Run([]string{"pack", "--descriptor", descPath, "--protocol", "example.custom/v1", "--binary", filepath.Join(source, "guest"), "--output", stage}, &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	installed, err := s.Install(stage)
	if err != nil {
		t.Fatal(err)
	}
	if code := manager.Run([]string{"doctor", "custom-provider"}, &out, &stderr); code == 0 {
		t.Fatal("untrusted package passed doctor")
	}
	if err := trust.Trust(installed); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := manager.Run([]string{"doctor", "custom-provider"}, &out, &stderr); code != 0 {
		t.Fatalf("doctor: %d: %s", code, &stderr)
	}
	if !strings.Contains(out.String(), "no guest executed") {
		t.Fatal(out.String())
	}
	if code := manager.Run([]string{"build", source, "--output", filepath.Join(root, "build.zip")}, &out, &stderr); code == 0 {
		t.Fatal("build selected an implicit toolchain")
	}
}

func TestSelectedAuthorManifest(t *testing.T) {
	root := t.TempDir()
	manifest := `{"apiVersion":"ext.io/v1","kind":"package","identity":{"id":"example/data","revision":"r1"},"contracts":[{"name":"example","version":"v1","operations":[{"name":"read"}]}],"artifacts":[{"name":"data","path":"data","sha256":""}]}`
	if err := os.WriteFile(filepath.Join(root, "package.custom"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := (Manager{}).Run([]string{"test", root, "--manifest", "package.custom"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if code := (Manager{}).Run([]string{"test", root}, &out, &stderr); code == 0 {
		t.Fatal("guessed a non-default manifest")
	}
}
