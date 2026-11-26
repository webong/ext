package author

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	data := `{"apiVersion":"ext.io/v1","kind":"package","identity":{"id":"example/provider","version":"1.0.0"},"contracts":[{"name":"example.custom","version":"v1","operations":[{"name":"call","surface":"action"}]}],"artifacts":[{"name":"main","path":"guest","sha256":""},{"name":"data","path":"assets","kind":"tree","sha256":""}],"entrypoints":[{"name":"main","runtime":"argv","artifact":"main","protocols":["example.custom/v1"]}],"metadata":{"example.settings/v1":{"enabled":true}}}`
	if err := os.WriteFile(filepath.Join(root, "ext.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "guest"), []byte("guest"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "data"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret"), []byte("not declared"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
func options(root string) Options { return Options{Source: root, OS: "linux", Arch: "amd64"} }
func TestPackArbitraryContractAndDeclaredTree(t *testing.T) {
	root := fixture(t)
	o := options(root)
	o.Output = filepath.Join(t.TempDir(), "package.zip")
	if err := Pack(o); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	names := map[string]bool{}
	for _, f := range z.File {
		names[f.Name] = true
		if f.Name == "ext.json" {
			r, _ := f.Open()
			var m packagekit.Manifest
			if err := json.NewDecoder(r).Decode(&m); err != nil {
				t.Fatal(err)
			}
			r.Close()
			if len(m.Metadata) != 1 || m.Descriptor.Contracts[0].Name != "example.custom" {
				t.Fatal("lost package declarations")
			}
		}
	}
	if len(names) != 3 || !names["ext.json"] || !names["guest"] || !names["assets/data"] {
		t.Fatalf("archive contains unexpected files: %v", names)
	}
	if err := Pack(o); err == nil {
		t.Fatal("overwrote output")
	}
	installedStore, err := store.NewWithLayout(filepath.Join(t.TempDir(), "packages"), store.Limits{}, storelayout.Ext())
	if err != nil {
		t.Fatal(err)
	}
	digest, err := packagekit.FileDigest(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := installedStore.InstallArchive(context.Background(), store.Archive{Source: o.Output, SHA256: digest}, store.ArchiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := installed.Verify(); err != nil {
		t.Fatal(err)
	}

	original, _ := os.ReadFile(filepath.Join(root, "ext.json"))
	if strings.Contains(string(original), `"revision"`) {
		t.Fatal("modified source")
	}
}
func TestPackRejectsUnsafeAndChangedArtifacts(t *testing.T) {
	for _, kind := range []string{"traversal", "symlink", "ancestor", "digest", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t)
			m, err := Read(root)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "traversal":
				m.Artifacts[0].Path = "../outside"
			case "manifest":
				m.Artifacts[0].Path = "ext.json"
			case "digest":
				m.Artifacts[0].SHA256 = strings.Repeat("1", 64)
			case "symlink":
				os.Remove(filepath.Join(root, "guest"))
				if err := os.Symlink("secret", filepath.Join(root, "guest")); err != nil {
					t.Skip(err)
				}
			case "ancestor":
				os.RemoveAll(filepath.Join(root, "assets"))
				outside := t.TempDir()
				os.WriteFile(filepath.Join(outside, "data"), []byte("outside"), 0o600)
				if err := os.Symlink(outside, filepath.Join(root, "assets")); err != nil {
					t.Skip(err)
				}
			}
			data, _ := json.Marshal(m)
			os.WriteFile(filepath.Join(root, "ext.json"), data, 0o600)
			stage, _, err := Prepare(options(root))
			if err == nil {
				os.RemoveAll(stage)
				t.Fatal("accepted unsafe artifacts")
			}
		})
	}
}
func TestSourceOnlyAndExplicitTargets(t *testing.T) {
	root := fixture(t)
	m, _ := Read(root)
	m.Entrypoints = nil
	m.Artifacts[0].OS = "windows"
	data, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(root, "ext.json"), data, 0o600)
	stage, sealed, err := Prepare(options(root))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if len(sealed.Artifacts) != 1 || sealed.Artifacts[0].Name != "data" || len(sealed.Entrypoints) != 0 {
		t.Fatal("target selection failed")
	}
	if err := sealed.VerifyArtifacts(stage); err != nil {
		t.Fatal(err)
	}
}
func TestAuthorBuildHelper(t *testing.T) {
	if os.Getenv("EXT_AUTHOR_HELPER") != "1" {
		return
	}
	if os.Getenv("GOWORK") != "off" || os.Getenv("EXT_PACKAGE_OS") != "linux" {
		os.Exit(3)
	}
	if err := os.WriteFile("guest", []byte("built"), 0o700); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
func TestBuildUsesExplicitCommandInIsolatedCopy(t *testing.T) {
	root := fixture(t)
	o := options(root)
	o.Output = filepath.Join(t.TempDir(), "built.zip")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_AUTHOR_HELPER", "1")
	if err := Build(context.Background(), o, []string{executable, "-test.run=^TestAuthorBuildHelper$"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "guest"))
	if string(data) != "guest" {
		t.Fatal("build changed source")
	}
	if err := Build(context.Background(), o, nil); err == nil {
		t.Fatal("accepted implicit build command")
	}
}

func TestSelectedManifestPath(t *testing.T) {
	root := fixture(t)
	if err := os.Mkdir(filepath.Join(root, "metadata"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "ext.json"), filepath.Join(root, "metadata", "package.json")); err != nil {
		t.Fatal(err)
	}
	o := options(root)
	o.ManifestPath = "metadata/package.json"
	o.Output = filepath.Join(t.TempDir(), "package.zip")
	if err := Pack(o); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	found := false
	for _, f := range z.File {
		if f.Name == o.ManifestPath {
			found = true
		}
		if f.Name == "ext.json" {
			t.Fatal("invented default manifest")
		}
	}
	if !found {
		t.Fatal("selected manifest missing")
	}
	if _, err := Read(root); err == nil {
		t.Fatal("default reader guessed a filename")
	}
}
